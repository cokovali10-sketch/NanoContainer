@echo off
echo Building NanoContainer...
go build -o nanocontainer.exe -ldflags="-s -w" ./cmd/nanocontainer
echo Done: nanocontainer.exe
