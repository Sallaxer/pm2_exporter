package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// PM2Process describes each process from `pm2 jlist`.
type PM2Process struct {
	PID    int    `json:"pid"`  // top-level "pid"
	Name   string `json:"name"` // top-level "name"`
	PM2Env struct {
		Status      string `json:"status"`
		RestartTime int64  `json:"restart_time"`
		CreatedAt   int64  `json:"created_at"` // Usually epoch ms
		PmUptime    int64  `json:"pm_uptime"`  // Typically epoch ms
		Versioning  struct {
			Type     string `json:"type"`
			URL      string `json:"url"`
			Branch   string `json:"branch"`
			Revision string `json:"revision"`
			Comment  string `json:"comment"`
		} `json:"versioning"`
	} `json:"pm2_env"`
	Monit struct {
		Memory int64   `json:"memory"` // bytes
		CPU    float64 `json:"cpu"`    // percent
	} `json:"monit"`
}

// SafePM2Data holds the data plus a timestamp.
type SafePM2Data struct {
	processes []PM2Process
	lastFetch time.Time
}

var (
	// CLI flags
	listenAddress  = flag.String("web.listen-address", ":9966", "Address on which to expose metrics and web interface (e.g. :9966).")
	scrapeInterval = flag.Int("web.scrape-interval", 30, "How often (in seconds) to run `pm2 jlist` in the background.")
	showHelp       = flag.Bool("help", false, "Show usage and exit.")

	pm2Data SafePM2Data
)

// sanitizeLabelValue ensures we don't break the Prometheus text format.
func sanitizeLabelValue(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	// Escape double quotes
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}

// fetchPM2Data runs `pm2 jlist` and updates pm2Data.
func fetchPM2Data() error {
	cmd := exec.Command("pm2", "jlist")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to run `pm2 jlist`: %v\noutput: %s", err, string(out))
	}

	var procs []PM2Process
	if err := json.Unmarshal(out, &procs); err != nil {
		return fmt.Errorf("failed to parse pm2 jlist JSON: %v", err)
	}

	pm2Data.processes = procs
	pm2Data.lastFetch = time.Now()
	return nil
}

// buildMetricsText composes the final text for `/metrics`.
func buildMetricsText() string {
	var sb strings.Builder

	// Collect lines for each metric
	var statusLines []string
	var branchLines []string
	var memoryLines []string
	var cpuLines []string
	var uptimeLines []string
	var restartLines []string
	var createdAtLines []string

	for _, p := range pm2Data.processes {
		pidStr := strconv.Itoa(p.PID)

		// pm2_status: 1 if status == "online", else 0
		var statusVal float64
		if p.PM2Env.Status == "online" {
			statusVal = 1
		} else {
			statusVal = 0
		}
		safeStatus := sanitizeLabelValue(p.PM2Env.Status)
		statusLines = append(statusLines, fmt.Sprintf(
			`pm2_status{process="%s",pid="%s",status="%s"} %g`,
			p.Name, pidStr, safeStatus, statusVal,
		))

		// pm2_branch_info: 1 if branch != "", else 0
		var branchValue float64
		if p.PM2Env.Versioning.Branch != "" {
			branchValue = 1
		}
		branchVal := sanitizeLabelValue(p.PM2Env.Versioning.Branch)
		revisionVal := sanitizeLabelValue(p.PM2Env.Versioning.Revision)
		commentVal := sanitizeLabelValue(p.PM2Env.Versioning.Comment)
		branchLines = append(branchLines, fmt.Sprintf(
			`pm2_branch_info{process="%s",pid="%s",branch="%s",revision="%s",comment="%s"} %g`,
			p.Name, pidStr, branchVal, revisionVal, commentVal, branchValue,
		))

		// pm2_memory_bytes
		memoryLines = append(memoryLines, fmt.Sprintf(
			`pm2_memory_bytes{process="%s",pid="%s"} %d`,
			p.Name, pidStr, p.Monit.Memory,
		))

		// pm2_cpu_percent
		cpuLines = append(cpuLines, fmt.Sprintf(
			`pm2_cpu_percent{process="%s",pid="%s"} %.2f`,
			p.Name, pidStr, p.Monit.CPU,
		))

		// pm2_uptime_seconds
		if p.PM2Env.PmUptime > 0 {
			msSince := time.Now().UnixMilli() - p.PM2Env.PmUptime
			if msSince < 0 {
				msSince = 0
			}
			uptimeSec := float64(msSince) / 1000.0
			uptimeLines = append(uptimeLines, fmt.Sprintf(
				`pm2_uptime_seconds{process="%s",pid="%s"} %.2f`,
				p.Name, pidStr, uptimeSec,
			))
		} else {
			uptimeLines = append(uptimeLines, fmt.Sprintf(
				`pm2_uptime_seconds{process="%s",pid="%s"} 0`,
				p.Name, pidStr,
			))
		}

		// pm2_restart_count
		restartLines = append(restartLines, fmt.Sprintf(
			`pm2_restart_count{process="%s",pid="%s"} %d`,
			p.Name, pidStr, p.PM2Env.RestartTime,
		))

		// pm2_created_at_timestamp
		createdAtLines = append(createdAtLines, fmt.Sprintf(
			`pm2_created_at_timestamp{process="%s",pid="%s"} %d`,
			p.Name, pidStr, p.PM2Env.CreatedAt,
		))
	}

	// Now group them:

	// pm2_status
	sb.WriteString(`# HELP pm2_status PM2 App process status: 1 if "online", 0 otherwise; label "status" shows the textual status
# TYPE pm2_status gauge
`)
	for _, line := range statusLines {
		sb.WriteString(line + "\n")
	}

	// pm2_branch_info
	sb.WriteString(`# HELP pm2_branch_info PM2 App processes branch, revision, and comment: 1 if branch is non-empty, else 0
# TYPE pm2_branch_info gauge
`)
	for _, line := range branchLines {
		sb.WriteString(line + "\n")
	}

	// pm2_memory_bytes
	sb.WriteString(`# HELP pm2_memory_bytes PM2 App process memory usage in bytes
# TYPE pm2_memory_bytes gauge
`)
	for _, line := range memoryLines {
		sb.WriteString(line + "\n")
	}

	// pm2_cpu_percent
	sb.WriteString(`# HELP pm2_cpu_percent PM2 App process CPU usage in percentage
# TYPE pm2_cpu_percent gauge
`)
	for _, line := range cpuLines {
		sb.WriteString(line + "\n")
	}

	// pm2_uptime_seconds
	sb.WriteString(`# HELP pm2_uptime_seconds PM2 App process uptime in seconds (calculated from "pm_uptime")
# TYPE pm2_uptime_seconds gauge
`)
	for _, line := range uptimeLines {
		sb.WriteString(line + "\n")
	}

	// pm2_restart_count
	sb.WriteString(`# HELP pm2_restart_count Number of restarts for a PM2 App process
# TYPE pm2_restart_count gauge
`)
	for _, line := range restartLines {
		sb.WriteString(line + "\n")
	}

	// pm2_created_at_timestamp
	sb.WriteString(`# HELP pm2_created_at_timestamp PM2 App process creation time in epoch milliseconds
# TYPE pm2_created_at_timestamp gauge
`)
	for _, line := range createdAtLines {
		sb.WriteString(line + "\n")
	}

	return sb.String()
}

// metricsHandler returns the cached data from pm2Data.
func metricsHandler(w http.ResponseWriter, r *http.Request) {
	metrics := buildMetricsText()
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	_, _ = w.Write([]byte(metrics))
}

// backgroundPoller runs fetchPM2Data() on a schedule.
func backgroundPoller(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// initial fetch
	if err := fetchPM2Data(); err != nil {
		log.Printf("Initial fetch error: %v", err)
	}

	for range ticker.C {
		if err := fetchPM2Data(); err != nil {
			log.Printf("fetchPM2Data error: %v", err)
		}
	}
}

// customUsage prints a custom usage text for `--help`.
func customUsage() {
	fmt.Fprintf(flag.CommandLine.Output(), `
PM2 Exporter for Prometheus

This exporter calls "pm2 jlist" periodically (default every 30 seconds),
parses the returned JSON, and exposes the data as Prometheus metrics.

Metrics include:
- pm2_status (1 if online, 0 otherwise, with label "status")
- pm2_branch_info (1 if branch is non-empty, 0 otherwise, with labels "branch", "revision", and "comment")
- pm2_memory_bytes
- pm2_cpu_percent
- pm2_uptime_seconds (calculated from pm_uptime)
- pm2_restart_count
- pm2_created_at_timestamp

All metrics also have "process" and "pid" labels to differentiate processes.

Usage:
`)
	flag.PrintDefaults()
	fmt.Println(`Example:
  ./pm2_exporter --web.listen-address=":9966" --web.scrape-interval=30

Flags:
  --web.listen-address    Address on which to expose metrics (default ":9966")
  --web.scrape-interval   How often (seconds) to call "pm2 jlist" (default 30)
  --help                  Show this help text`)
}

func main() {
	// Override default usage
	flag.Usage = customUsage
	flag.Parse()

	if *showHelp {
		customUsage()
		os.Exit(0)
	}

	// Start background fetch
	go backgroundPoller(time.Duration(*scrapeInterval) * time.Second)

	// HTTP route
	http.HandleFunc("/metrics", metricsHandler)

	log.Printf("Starting PM2 exporter on %s, scraping every %d seconds...", *listenAddress, *scrapeInterval)
	err := http.ListenAndServe(*listenAddress, nil)
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}
}
