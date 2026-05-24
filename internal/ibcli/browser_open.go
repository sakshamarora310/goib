package ibcli

import (
	"os/exec"
	"runtime"
)

func openBrowser(target string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	case "darwin":
		cmd = exec.Command("open", target)
	default:
		path, err := exec.LookPath("xdg-open")
		if err != nil {
			return errNoBrowser
		}
		cmd = exec.Command(path, target)
	}
	return cmd.Start()
}
