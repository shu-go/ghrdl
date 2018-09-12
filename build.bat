go build -ldflags "-w -s" %*
if errorlevel 1 pause
