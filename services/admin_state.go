package services

import (
	"sync"
	"time"
)

type AdminStateStep int

const (
	StepNone AdminStateStep = iota
	StepBlogAddTitle
	StepBlogAddContent
	StepBlogEditTitle
	StepBlogEditContent
)

type AdminState struct {
	Step      AdminStateStep
	BlogID    int
	TempTitle string
	ExpiresAt time.Time
}

var (
	adminStates = make(map[int64]*AdminState)
	adminMu     sync.RWMutex
)

func SetAdminState(userID int64, state *AdminState) {
	adminMu.Lock()
	defer adminMu.Unlock()

	state.ExpiresAt = time.Now().Add(10 * time.Minute)
	adminStates[userID] = state
}

func GetAdminState(userID int64) *AdminState {
	adminMu.RLock()
	defer adminMu.RUnlock()

	state, exists := adminStates[userID]
	if !exists {
		return nil
	}

	if time.Now().After(state.ExpiresAt) {
		delete(adminStates, userID)
		return nil
	}

	return state
}

func ClearAdminState(userID int64) {
	adminMu.Lock()
	defer adminMu.Unlock()

	delete(adminStates, userID)
}
