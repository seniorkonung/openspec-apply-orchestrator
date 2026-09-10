package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	workingDirectory, err := os.Getwd()
	if err != nil {
		newCommandReporter(os.Stdout, false).line("Ошибка: не удалось определить рабочий каталог.")
		os.Exit(exitObstacle)
	}

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)

	code := runWithSignals(signals, func(ctx context.Context) int {
		return runCommand(
			ctx,
			os.Args[1:],
			workingDirectory,
			os.Stdout,
			productionCommandDependencies(),
		)
	})
	os.Exit(code)
}
