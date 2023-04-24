// Copyright Elasticsearch B.V. and/or licensed to Elasticsearch B.V. under one
// or more contributor license agreements. Licensed under the Elastic License;
// you may not use this file except in compliance with the Elastic License.

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/elastic/fleet-server/v7/internal/pkg/dl"
	"github.com/elastic/fleet-server/v7/internal/pkg/limit"
	"github.com/elastic/fleet-server/v7/internal/pkg/logger"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/hlog"
)

// Alias logger constants
const (
	ECSHTTPRequestID         = logger.ECSHTTPRequestID
	ECSEventDuration         = logger.ECSEventDuration
	ECSHTTPResponseCode      = logger.ECSHTTPResponseCode
	ECSHTTPResponseBodyBytes = logger.ECSHTTPResponseBodyBytes

	LogAPIKeyID       = logger.APIKeyID
	LogPolicyID       = logger.PolicyID
	LogAgentID        = logger.AgentID
	LogEnrollAPIKeyID = logger.EnrollAPIKeyID
	LogAccessAPIKeyID = logger.AccessAPIKeyID
)

// HTTPErrResp is an HTTP error response
type HTTPErrResp struct {
	StatusCode int           `json:"statusCode"`
	Error      string        `json:"error"`
	Message    string        `json:"message,omitempty"`
	Level      zerolog.Level `json:"-"`
}

// NewHTTPErrResp creates an ErrResp from a go error
func NewHTTPErrResp(err error) HTTPErrResp {
	errTable := []struct {
		target error
		meta   HTTPErrResp
	}{
		{
			ErrAgentNotFound,
			HTTPErrResp{
				http.StatusNotFound,
				"AgentNotFound",
				"agent could not be found",
				zerolog.WarnLevel,
			},
		},
		{
			ErrAPIKeyNotEnabled,
			HTTPErrResp{
				http.StatusUnauthorized,
				"Unauthorized",
				"ApiKey not enabled",
				zerolog.InfoLevel,
			},
		},
		{
			context.Canceled,
			HTTPErrResp{
				http.StatusServiceUnavailable,
				"ServiceUnavailable",
				"server is stopping",
				zerolog.DebugLevel,
			},
		},
		{
			ErrInvalidUserAgent,
			HTTPErrResp{
				http.StatusBadRequest,
				"InvalidUserAgent",
				"user-agent is invalid",
				zerolog.InfoLevel,
			},
		},
		{
			ErrUnsupportedVersion,
			HTTPErrResp{
				http.StatusBadRequest,
				"UnsupportedVersion",
				"version is not supported",
				zerolog.InfoLevel,
			},
		},
		{
			dl.ErrNotFound,
			HTTPErrResp{
				http.StatusNotFound,
				"NotFound",
				"not found",
				zerolog.WarnLevel,
			},
		},
		{
			ErrorThrottle,
			HTTPErrResp{
				http.StatusTooManyRequests,
				"TooManyRequests",
				"too many requests",
				zerolog.DebugLevel,
			},
		},
		{
			limit.ErrRateLimit,
			HTTPErrResp{
				http.StatusTooManyRequests,
				"RateLimit",
				"exceeded the rate limit",
				zerolog.WarnLevel,
			},
		},
		{
			limit.ErrMaxLimit,
			HTTPErrResp{
				http.StatusTooManyRequests,
				"MaxLimit",
				"exceeded the max limit",
				zerolog.WarnLevel,
			},
		},
		{
			os.ErrDeadlineExceeded,
			HTTPErrResp{
				http.StatusRequestTimeout,
				"RequestTimeout",
				"timeout on request",
				zerolog.InfoLevel,
			},
		},
		{
			ErrUpdatingInactiveAgent,
			HTTPErrResp{
				http.StatusUnauthorized,
				"Unauthorized",
				"Agent not active",
				zerolog.InfoLevel,
			},
		},
	}

	for _, e := range errTable {
		if errors.Is(err, e.target) {
			return e.meta
		}
	}

	// If it's a JSON marshal error
	var jErr *json.MarshalerError
	if errors.As(err, &jErr) {
		return HTTPErrResp{
			http.StatusInternalServerError,
			err.Error(),
			"Fleet server unable to marshall JSON",
			zerolog.ErrorLevel,
		}
	}

	// Check if we have encountered a connectivity error
	// Predicate taken from https://github.com/golang/go/blob/go1.17.5/src/net/dial_test.go#L798
	if strings.Contains(err.Error(), "connection refused") {
		return HTTPErrResp{
			http.StatusServiceUnavailable,
			"ServiceUnavailable",
			"Fleet server unable to communicate with Elasticsearch",
			zerolog.InfoLevel,
		}
	}

	// Default
	return HTTPErrResp{
		StatusCode: http.StatusBadRequest,
		Error:      "BadRequest",
		Message:    err.Error(),
		Level:      zerolog.InfoLevel,
	}
}

// Write will serialize the ErrResp to an http response and include the proper headers.
func (er HTTPErrResp) Write(w http.ResponseWriter) error {
	data, err := json.Marshal(&er)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(er.StatusCode)
	_, err = w.Write(data)
	return err
}

func ErrorResp(w http.ResponseWriter, r *http.Request, err error) {
	zlog := hlog.FromRequest(r)
	resp := NewHTTPErrResp(err)
	e := zlog.WithLevel(resp.Level).Err(err).Int(ECSHTTPResponseCode, resp.StatusCode)
	if ts, ok := logger.CtxStartTime(r.Context()); ok {
		e = e.Int64(ECSEventDuration, time.Since(ts).Nanoseconds())
	}
	e.Msg("HTTP request error")
	if rerr := resp.Write(w); rerr != nil {
		zlog.Error().Err(rerr).Msg("fail writing error response")
	}
}
