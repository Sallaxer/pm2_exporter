VERSION := 0.1.0
FLAGS := CGO_ENABLED=0
build:
    @echo ">> building binaries" 
	$(FLAGS) go build -mod=mod -ldflags "-s -w -X main.version=$(VERSION)" -o pm2_exporter

run: build 
	./pm2_exporter

clean: 
	rm -f ./pm2_exporter