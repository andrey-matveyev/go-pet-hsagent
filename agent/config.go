package main

type config struct {
	Listener struct {
		ListenAddress string `toml:"listen_address"`
	} `toml:"listener"`
	Backup struct {
		SourceDirectory      string `toml:"source_directory"`
		TargetFilesystemUUID string `toml:"target_filesystem_uuid"`
		TargetMountPoint     string `toml:"target_mount_point"`
		TargetXhciPCI        string `toml:"target_xhci_pci"`
		ScheduleWeekday      string `toml:"schedule_weekday"`
		ScheduleTime         string `toml:"schedule_time"`
	} `toml:"backup"`
	Monitoring struct {
		Battery    int     `toml:"battery"`
		Cpu        int     `toml:"cpu"`
		Disk       int     `toml:"disk"`
		NeedReboot int     `toml:"need_reboot"`
	} `toml:"monitoring"`
	Limits struct {
		CpuMaxTemperature   float64 `toml:"cpu_max_temperature"`
		DiskMinFreePercent  float64 `toml:"disk_min_free_percent"`
	} `toml:"limits"`
}
