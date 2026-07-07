package services

import (
	"fmt"
	"sync"
)

// outageAlertThreshold is the default number of consecutive failures after
// which a service is considered down and the admin is alerted.
const outageAlertThreshold = 3

type serviceHealthEntry struct {
	consecutiveFailures int
	alerted             bool
}

var (
	serviceHealthMu    sync.Mutex
	serviceHealthState = make(map[string]*serviceHealthEntry)
)

// ReportServiceFailure records a failure of an external service with the
// default outage threshold.
func ReportServiceFailure(service, detail string) {
	ReportServiceFailureWithThreshold(service, detail, outageAlertThreshold)
}

// ReportServiceFailureWithThreshold records a failure of an external service.
// Once failures become consecutive enough to look like an outage, the admin is
// notified (NotifyAdmin's rate limit turns this into a periodic reminder while
// the outage lasts).
func ReportServiceFailureWithThreshold(service, detail string, threshold int) {
	serviceHealthMu.Lock()

	entry, exists := serviceHealthState[service]

	if !exists {
		entry = &serviceHealthEntry{}
		serviceHealthState[service] = entry
	}

	entry.consecutiveFailures++
	failures := entry.consecutiveFailures
	shouldAlert := failures >= threshold

	if shouldAlert {
		entry.alerted = true
	}

	serviceHealthMu.Unlock()

	if shouldAlert {
		text := fmt.Sprintf("⚠️ %s looks DOWN: %d consecutive failures.\nLast error: %s", service, failures, detail)
		NotifyAdmin("outage:"+service, text)
	}
}

// ReportServiceSuccess records a success of an external service and notifies
// the admin about the recovery if an outage alert was previously sent. The
// outage alert's rate-limit stamp is cleared so a repeat outage within the
// rate-limit window still alerts immediately.
func ReportServiceSuccess(service string) {
	serviceHealthMu.Lock()

	entry, exists := serviceHealthState[service]
	wasAlerted := exists && entry.alerted

	if exists {
		entry.consecutiveFailures = 0
		entry.alerted = false
	}

	serviceHealthMu.Unlock()

	if wasAlerted {
		ResetAdminAlert("outage:" + service)
		NotifyAdmin("recovery:"+service, fmt.Sprintf("✅ %s recovered and is resolving media again.", service))
	}
}
