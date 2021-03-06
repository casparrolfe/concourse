package builds

import (
	"os"
	"time"

	"code.cloudfoundry.org/clock"
	"code.cloudfoundry.org/lager"
)

//go:generate counterfeiter . BuildTracker

type BuildTracker interface {
	Track()
	Release()
}

//go:generate counterfeiter . Notifications

type Notifications interface {
	Listen(channel string) (chan bool, error)
	Unlisten(channel string, notifier chan bool) error
}

type TrackerRunner struct {
	Tracker       BuildTracker
	Notifications Notifications
	Interval      time.Duration
	Clock         clock.Clock
	DrainCh       <-chan struct{}
	Logger        lager.Logger
}

func (runner TrackerRunner) Run(signals <-chan os.Signal, ready chan<- struct{}) error {

	shutdownNotifier, err := runner.Notifications.Listen("atc_shutdown")
	if err != nil {
		return err
	}

	defer runner.Notifications.Unlisten("atc_shutdown", shutdownNotifier)

	buildNotifier, err := runner.Notifications.Listen("build_started")
	if err != nil {
		return err
	}

	defer runner.Notifications.Unlisten("build_started", buildNotifier)

	ticker := runner.Clock.NewTicker(runner.Interval)

	close(ready)

	runner.Tracker.Track()

	for {
		select {
		case <-runner.DrainCh:
			return nil
		case <-shutdownNotifier:
			runner.Logger.Info("received-atc-shutdown-message")
			runner.Tracker.Track()
		case <-buildNotifier:
			runner.Logger.Info("received-build-started-message")
			runner.Tracker.Track()
		case <-ticker.C():
			runner.Tracker.Track()
		case <-signals:
			return nil
		}
	}
}
