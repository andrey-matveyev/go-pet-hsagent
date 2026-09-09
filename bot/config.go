package main

var (
	version   = "unknown" // Overwritten via -ldflags
	gitCommit = "unknown" // Overwritten via -ldflags
	buildDate = "unknown" // Overwritten via -ldflags

	tgBotToken string
	chatID     int64
	// For local tests use 127.0.0.1
	// For CasaOS change this address to host.docker.internal:50051
	agentAddr = "host.docker.internal:50051"
)
