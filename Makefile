vet:
	go vet ./...

run:
	go run .

build:
	go build .

all: vet build
