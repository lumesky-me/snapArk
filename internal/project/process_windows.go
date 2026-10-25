package project

import (
	"os/exec"
	"strconv"
	"time"
)

func configureProcess(c *exec.Cmd) {
	c.Cancel = func() error { return exec.Command("taskkill", "/PID", strconv.Itoa(c.Process.Pid), "/T", "/F").Run() }
	c.WaitDelay = 2 * time.Second
}
