package main

type config struct {
	ListenAddress string `toml:"listen_address"`
	Backup        struct {
		UUID            string `toml:"uuid"`
		SourceDir       string `toml:"source_dir"`
		MountPoint      string `toml:"mount_point"`
		XhciPCI         string `toml:"xhci_pci"`
		ScheduleWeekday string `toml:"schedule_weekday"`
		ScheduleTime    string `toml:"schedule_time"`
	} `toml:"backup"`
	Monitoring struct {
		BatteryIntervalMin int     `toml:"battery_interval_min"`
		CpuIntervalMin     int     `toml:"cpu_interval_min"`
		DiskIntervalMin    int     `toml:"disk_interval_min"`
		CpuTempThreshold   float64 `toml:"cpu_temp_threshold"`
		DiskMinFreePercent float64 `toml:"disk_min_free_percent"`
	} `toml:"monitoring"`
}
