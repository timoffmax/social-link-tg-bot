package dashboard

import (
	"context"
	"net/http"
)

// handleHealth answers ok when the database answers a ping.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	load := func(ctx context.Context) (payload, error) {
		err := s.store.Ping(ctx)
		data := &Health{OK: true}

		return data, err
	}

	s.respondLoaded(w, r, http.StatusOK, load)
}

// handleOverview serves GET /api/overview?days=.
func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	days, err := overviewDaysParam.parse(query)
	load := func(ctx context.Context) (payload, error) {
		data, loadErr := s.store.Overview(ctx, days)

		return &data, loadErr
	}

	s.respond(w, r, err, load)
}

// handlePlatforms serves GET /api/platforms?days=.
func (s *Server) handlePlatforms(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	days, err := platformsDaysParam.parse(query)
	load := func(ctx context.Context) (payload, error) {
		data, loadErr := s.store.Platforms(ctx, days)

		return &data, loadErr
	}

	s.respond(w, r, err, load)
}

// handleChats serves GET /api/chats?days=&limit=.
func (s *Server) handleChats(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	days, daysErr := chatsDaysParam.parse(query)
	limit, limitErr := chatsLimitParam.parse(query)
	err := firstError(daysErr, limitErr)
	load := func(ctx context.Context) (payload, error) {
		data, loadErr := s.store.Chats(ctx, days, limit)

		return &data, loadErr
	}

	s.respond(w, r, err, load)
}

// handleMoney serves GET /api/money?months=.
func (s *Server) handleMoney(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	months, err := moneyMonthsParam.parse(query)
	load := func(ctx context.Context) (payload, error) {
		data, loadErr := s.store.Money(ctx, months)

		return &data, loadErr
	}

	s.respond(w, r, err, load)
}

// handleReports serves GET /api/reports?limit=&weeks=&status=.
func (s *Server) handleReports(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	limit, limitErr := reportsLimitParam.parse(query)
	weeks, weeksErr := reportsWeeksParam.parse(query)
	status, statusErr := parseReportStatus(r)
	err := firstError(limitErr, weeksErr, statusErr)
	load := func(ctx context.Context) (payload, error) {
		data, loadErr := s.store.Reports(ctx, limit, weeks, status)
		data.ReplyPrefix = ReplyMessagePrefix
		data.ReplyTextLimit = ReplyTextLimit

		return &data, loadErr
	}

	s.respond(w, r, err, load)
}
