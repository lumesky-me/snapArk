package project

import (
	"github.com/lumesky-me/veyra/internal/backend"
	"os/exec"
	"strconv"
	"time"
)

func configureProcess(c *exec.Cmd) {
	c.Cancel = func() error {
		kill := exec.Command("taskkill", "/PID", strconv.Itoa(c.Process.Pid), "/T", "/F")
		kill.Env = backend.Environment()
		return kill.Run()
	}
	c.WaitDelay = 2 * time.Second
}
