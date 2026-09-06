package main

import (
	"bytes"
	"context"
	"os/exec"
	"regexp"
	"strings"
)

func runCmd(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return strings.TrimSpace(out.String()), err
}

// Надежная обертка над exec.Command с поддержкой Контекста (таймаутов)
func runCmdWithContext(ctx context.Context, name string, arg ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, arg...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// Безопасное извлечение базового имени диска из имени раздела
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
