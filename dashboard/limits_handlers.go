package dashboard

import (
	"context"
	"net/http"
)

// allowLimitRequests is the Allow header of the limit request route.
const allowLimitRequests = "POST"

// newLimitsRoutes binds the Limits handlers: two read routes and the
// limit request write route.
func (s *Server) newLimitsRoutes() {
	s.routes[limitsPath] = s.handleLimits
	s.routes[limitScopePath] = s.handleLimitScope
	s.limitRequests = writeRoute{
		handlers: map[string]routeHandler{http.MethodPost: s.handleQueueLimitRequest},
		allow:    allowLimitRequests,
	}
}

// matchLimitsRoute matches "/api/limits/requests"; false for any other path.
func (s *Server) matchLimitsRoute(urlPath string) (writeRoute, bool) {
	isMatch := (urlPath == limitRequestsPath)

	return s.limitRequests, isMatch
}

// handleLimits serves GET /api/limits?limit=.
func (s *Server) handleLimits(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	rows, err := limitRowsParam.parse(query)
	now := s.now()
	load := func(ctx context.Context) (payload, error) {
		snapshot, loadErr := s.store.Limits(ctx, rows)
		data := &LimitsPage{Rows: []LimitRow{}}

		if loadErr == nil {
			data, loadErr = buildLimitsPage(snapshot, now)
		}

		return data, loadErr
	}

	s.respond(w, r, err, load)
}

// handleLimitScope serves GET /api/limits/scope?scope=&id=: one person or
// group as stored, for find by id.
func (s *Server) handleLimitScope(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	scopeText := query.Get(paramScope)
	idText := query.Get(paramID)
	scope, id, err := parseLimitScope(scopeText, idText)
	now := s.now()
	load := func(ctx context.Context) (payload, error) {
		snapshot, loadErr := s.store.LimitScope(ctx, scope, id)
		data := &LimitScopeResponse{}

		if loadErr == nil {
			data, loadErr = buildLimitScope(snapshot, now)
		}

		return data, loadErr
	}

	s.respond(w, r, err, load)
}

// handleQueueLimitRequest serves POST /api/limits/requests: checks the
// request with the rules the bot published and queues it for the bot (202).
func (s *Server) handleQueueLimitRequest(w http.ResponseWriter, r *http.Request, id int64) {
	body := limitRequestBody{}
	err := decodeJSONBody(w, r, &body)
	load := func(ctx context.Context) (payload, error) {
		registry, loadErr := s.store.SettingsRegistry(ctx)
		data := &SettingRequestAnswer{}
		draft := SettingRequestDraft{}

		if loadErr == nil {
			rules := supportedLimitRules(registry)
			draft, loadErr = limitRequestDraft(body, rules)
		}

		if loadErr == nil {
			data.Request, loadErr = s.store.QueueLimitRequest(ctx, draft)
		}

		return data, loadErr
	}

	s.respondWithStatus(w, r, http.StatusAccepted, err, load)
}
