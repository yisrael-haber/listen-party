run: compile
	clear && ./build/lp

compile:
	clear && date
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o build/lp .
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o build/lp.exe .
	