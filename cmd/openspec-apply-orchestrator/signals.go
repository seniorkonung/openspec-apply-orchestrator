package main

import (
	"context"
	"os"
	"syscall"
)

func runWithSignals(signals <-chan os.Signal, command func(context.Context) int) int {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan int, 1)
	go func() {
		result <- command(ctx)
	}()

	select {
	case code := <-result:
		return code
	case received := <-signals:
		cancel()
		<-result
		return signalExitCode(received)
	}
}

func signalExitCode(signal os.Signal) int {
	if number, ok := signal.(syscall.Signal); ok {
		return 128 + int(number)
	}
	return exitObstacle
}
