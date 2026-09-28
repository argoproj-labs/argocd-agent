package main

import (
	"fmt"
	"time"
)

// These constants control behaviour of the utility as it runs
const (
	outputVerboseText        bool = true  // default: true. if true, output events in all cases, not just on fail
	outputTextWhileRunning   bool = false // default: false. if true, output create/modify/delete events as the utility runs
	maxPropagationDelayCheck bool = true  // default: true. if true, validate source -> destination consistency.

	// Min/max number of Applications to simulate
	minConcurrentAppsToCreate = 110 // default: 110
	maxConcurrentApps         = 200 // default: 200

	// % chance for spec writer to create a new app/update existing app/delete an existing app
	// - if an action is not possible (for example, deleting an app when no apps exist), it will be skipped (without reporting an error)
	specWriterCreatePercent = 10 // default: 10
	specWriterUpdatePercent = 87 // default: 87
	specWriterDeletePercent = 3  // default: 3

	// Time to run before verifying results
	timeToRun = 60 * time.Second // default: 60

	// maxPropagationDelay is the length of time we wait for an event to be transmitted from source, then processed on destination, before we mark it as permanently missed.
	// e.g. if a .spec update is made on Application on principal, and the corresponding change is NOT made on managed agent Application within (e.g.) 15 seconds, we will assume that event was permanently lost (which is bad)
	maxPropagationDelay = 15 * time.Second // default: 15 * time.Second

	// QPS indicates the maximum QPS to the master from this client.
	// If it's zero, the created RESTClient will use DefaultQPS: 5
	//
	// Setting this to a negative value will disable client-side ratelimiting
	// unless `Ratelimiter` is also set.
	clientQPS float32 = 50 // default: 50

	// Maximum burst for throttle.
	// If it's zero, the created RESTClient will use DefaultBurst: 10.
	clientBurst int = 0 // default: 0

	// Rate limiter for limiting connections to the master from this client. If true, overwrites QPS/Burst
	disableClientRateLimiter bool = false // default: false
)

func init() {
	if specWriterCreatePercent+specWriterUpdatePercent+specWriterDeletePercent != 100 {
		panic("specWriter percentages must sum to 100")
	}
}

func main() {

	err := run()
	if err != nil {
		exit(fmt.Sprintf("%v", err))
	}

	// Wait for os.Exit() call elsewhere in the code.
	for {
		time.Sleep(1 * time.Second)
	}
}
