package main

import (
	"bytes"
	"context"
	"io/ioutil"
	"os/exec"
	"regexp"
	"strings"
)

// SystemRunner абстрагирует консольные команды и работу с файловой системой /sys
type SystemRunner interface {
	RunCmdWithContext(ctx context.Context, name string, arg ...string) (string, error)
	ReadFile(filename string) ([]byte, error)
}

// DefaultSystemRunner — реализация по умолчанию для работы на реальном Linux сервере
type DefaultSystemRunner struct{}

func (d *DefaultSystemRunner) RunCmdWithContext(ctx context.Context, name string, arg ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, arg...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (d *DefaultSystemRunner) ReadFile(filename string) ([]byte, error) {
	return ioutil.ReadFile(filename)
}

func runCmd(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return strings.TrimSpace(out.String()), err
}

func getBaseDevice(devPath string) string {
	devPath = strings.TrimSpace(devPath)
	if devPath == "" {
		return ""
	}
	// Регулярное выражение отсекает суффиксы разделов вроде p1, 1, 2 для sdX и nvmeXnXpX
	re := regexp.MustCompile(`(^/dev/nvme\d+n\d+)(p\d+)?$|(^/dev/sd[a-z])(\d+)?$`)
	matches := re.FindStringSubmatch(devPath)

	if len(matches) > 0 {
		if matches[1] != "" {
			return matches[1]
		} // Для NVMe
		if matches[3] != "" {
			return matches[3]
		} // Для SATA/USB жестких дисков
	}
	// Фолбэк: если регулярка не совпала, убираем цифры с конца (старый метод как запасной)
	return strings.TrimRight(devPath, "0123456789")
}
