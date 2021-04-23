go build -trimpath -ldflags "-w -s" %*
if errorlevel 1 pause
