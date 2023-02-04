// Copyright Elasticsearch B.V. and/or licensed to Elasticsearch B.V. under one
// or more contributor license agreements. Licensed under the Elastic License;
// you may not use this file except in compliance with the Elastic License.

package api

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"time"

	"github.com/elastic/fleet-server/v7/internal/pkg/action"
	"github.com/elastic/fleet-server/v7/internal/pkg/actor"
	"github.com/elastic/fleet-server/v7/internal/pkg/bulk"
	"github.com/elastic/fleet-server/v7/internal/pkg/cache"
	"github.com/elastic/fleet-server/v7/internal/pkg/checkin"
	"github.com/elastic/fleet-server/v7/internal/pkg/config"
	"github.com/elastic/fleet-server/v7/internal/pkg/dl"
	"github.com/elastic/fleet-server/v7/internal/pkg/logger"
	"github.com/elastic/fleet-server/v7/internal/pkg/model"
	"github.com/elastic/fleet-server/v7/internal/pkg/monitor"
	"github.com/elastic/fleet-server/v7/internal/pkg/policy"
	"github.com/elastic/fleet-server/v7/internal/pkg/sqn"
	"github.com/ergo-services/ergo/gen"

	"github.com/hashicorp/go-version"
	"github.com/julienschmidt/httprouter"
	"github.com/miolini/datacounter"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

var (
	ErrAgentNotFound    = errors.New("agent not found")
	ErrFailInjectAPIKey = errors.New("failure to inject api key")
)

const (
	kEncodingGzip = "gzip"
)

//nolint:dupl // function body calls different internal hander then handleAck
func (rt *Router) handleCheckin(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	start := time.Now()

	id := ps.ByName("id")

	reqID := r.Header.Get(logger.HeaderRequestID)

	zlog := log.With().
		Str(LogAgentID, id).
		Str(ECSHTTPRequestID, reqID).
		Logger()

	err := rt.ct.handleCheckin(&zlog, w, r, id)
	if err != nil {
		cntCheckin.IncError(err)
		resp := NewHTTPErrResp(err)

		zlog.WithLevel(resp.Level).
			Err(err).
			Int(ECSHTTPResponseCode, resp.StatusCode).
			Int64(ECSEventDuration, time.Since(start).Nanoseconds()).
			Msg("fail checkin")

		if err := resp.Write(w); err != nil {
			zlog.Error().Err(err).Msg("fail writing error response")
		}
	}
}

type CheckinT struct {
	checkin.CheckinContext
	verCon version.Constraints
	// cfg    *config.Server
	cache cache.Cache
	// bc     *checkin.Bulk
	// pm     policy.Monitor
	// gcp    monitor.GlobalCheckpointProvider
	// ad     *action.Dispatcher
	tr *action.TokenResolver
	// bulker bulk.Bulk
	sup *gen.Process
}

func NewCheckinT(
	verCon version.Constraints,
	cfg *config.Server,
	c cache.Cache,
	bc *checkin.Bulk,
	pm policy.Monitor,
	gcp monitor.GlobalCheckpointProvider,
	ad *action.Dispatcher,
	tr *action.TokenResolver,
	bulker bulk.Bulk,
	sup *gen.Process,
) *CheckinT {
	ct := &CheckinT{
		CheckinContext: checkin.CheckinContext{
			Bc:     bc,
			Pm:     pm,
			Ad:     ad,
			Bulker: bulker,
			Gcp:    gcp,
			Cfg:    cfg,
		},
		sup:    sup,
		verCon: verCon,
		cache:  c,
		tr:     tr,
	}

	return ct
}

func (ct *CheckinT) handleCheckin(zlog *zerolog.Logger, w http.ResponseWriter, r *http.Request, id string) error {
	start := time.Now()

	agent, err := authAgent(r, &id, ct.Bulker, ct.cache)
	if err != nil {
		return err
	}

	// Pointer is passed in to allow UpdateContext by child function
	zlog.UpdateContext(func(ctx zerolog.Context) zerolog.Context {
		return ctx.Str(LogAccessAPIKeyID, agent.AccessAPIKeyID)
	})

	ver, err := validateUserAgent(*zlog, r, ct.verCon)
	if err != nil {
		return err
	}

	// Safely check if the agent version is different, return empty string otherwise
	newVer := agent.CheckDifferentVersion(ver)
	return ct.ProcessRequest(*zlog, w, r, start, agent, newVer)
}

func (ct *CheckinT) ProcessRequest(zlog zerolog.Logger, w http.ResponseWriter, r *http.Request, start time.Time, agent *model.Agent, ver string) error {

	ctx := r.Context()

	body := r.Body

	// Limit the size of the body to prevent malicious agent from exhausting RAM in server
	if ct.Cfg.Limits.CheckinLimit.MaxBody > 0 {
		body = http.MaxBytesReader(w, body, ct.Cfg.Limits.CheckinLimit.MaxBody)
	}

	readCounter := datacounter.NewReaderCounter(body)

	var req checkin.CheckinRequest
	decoder := json.NewDecoder(readCounter)
	if err := decoder.Decode(&req); err != nil {
		return fmt.Errorf("decode checkin request: %w", err)
	}

	cntCheckin.bodyIn.Add(readCounter.Count())

	// Compare local_metadata content and update if different
	rawMeta, err := parseMeta(zlog, agent, &req)
	if err != nil {
		return err
	}

	// Compare agent_components content and update if different
	rawComponents, err := parseComponents(zlog, agent, &req)
	if err != nil {
		return err
	}

	// Resolve AckToken from request, fallback on the agent record
	seqno, err := ct.resolveSeqNo(ctx, zlog, req, agent)
	if err != nil {
		return err
	}

	_, err = (*ct.sup).Direct(actor.AddAgentMessage{Id: agent.Id})
	if err != nil {
		return err
	}
	defer (*ct.sup).Direct(actor.RemoveAgentMessage{Id: agent.Id})

	reqExt := checkin.NewCheckinRequestExt(
		&req,
		agent,
		start,
		ver,
		rawMeta,
		rawComponents,
		seqno,
	)

	// resp, err := (*ct.sup).DirectWithTimeout(actor.GetNextUpdate{Ctx: &ct.CheckinContext, Req: reqExt}, int(ct.Cfg.Timeouts.CheckinLongPoll.Seconds()))
	resp, err := checkin.GetNextUpdate(ctx, zlog, &ct.CheckinContext, reqExt)
	if err != nil {
		return fmt.Errorf("direct getnextupdate error: %v", err)
	}

	// return ct.writeResponse(zlog, w, r, resp.(*checkin.CheckinResponse))
	return ct.writeResponse(zlog, w, r, resp)
}

func (ct *CheckinT) writeResponse(zlog zerolog.Logger, w http.ResponseWriter, r *http.Request, resp *checkin.CheckinResponse) error {

	payload, err := json.Marshal(&resp)
	if err != nil {
		return fmt.Errorf("writeResponse marshal: %w", err)
	}

	compressionLevel := ct.Cfg.CompressionLevel
	compressThreshold := ct.Cfg.CompressionThresh

	if len(payload) > compressThreshold && compressionLevel != flate.NoCompression && acceptsEncoding(r, kEncodingGzip) {

		wrCounter := datacounter.NewWriterCounter(w)

		zipper, err := gzip.NewWriterLevel(wrCounter, compressionLevel)
		if err != nil {
			return fmt.Errorf("writeResponse new gzip: %w", err)
		}

		w.Header().Set("Content-Encoding", kEncodingGzip)

		if _, err = zipper.Write(payload); err != nil {
			return fmt.Errorf("writeResponse gzip write: %w", err)
		}

		if err = zipper.Close(); err != nil {
			err = fmt.Errorf("writeResponse gzip close: %w", err)
		}

		cntCheckin.bodyOut.Add(wrCounter.Count())

		zlog.Trace().
			Err(err).
			Int("lvl", compressionLevel).
			Int("srcSz", len(payload)).
			Uint64("dstSz", wrCounter.Count()).
			Msg("compressing checkin response")
	} else {
		var nWritten int
		nWritten, err = w.Write(payload)
		cntCheckin.bodyOut.Add(uint64(nWritten))

		if err != nil {
			err = fmt.Errorf("writeResponse payload: %w", err)
		}
	}

	return err
}

func acceptsEncoding(r *http.Request, encoding string) bool {
	for _, v := range r.Header.Values("Accept-Encoding") {
		if v == encoding {
			return true
		}
	}
	return false
}

// Resolve AckToken from request, fallback on the agent record
func (ct *CheckinT) resolveSeqNo(ctx context.Context, zlog zerolog.Logger, req checkin.CheckinRequest, agent *model.Agent) (sqn.SeqNo, error) {
	var err error
	// Resolve AckToken from request, fallback on the agent record
	ackToken := req.AckToken
	var seqno sqn.SeqNo = agent.ActionSeqNo

	if ct.tr != nil && ackToken != "" {
		var sn int64
		sn, err = ct.tr.Resolve(ctx, ackToken)
		if err != nil {
			if errors.Is(err, dl.ErrNotFound) {
				zlog.Debug().Str("token", ackToken).Msg("revision token not found")
				err = nil
			} else {
				return seqno, fmt.Errorf("resolveSeqNo: %w", err)
			}
		}
		seqno = []int64{sn}
	}
	return seqno, err
}

func findAgentByAPIKeyID(ctx context.Context, bulker bulk.Bulk, id string) (*model.Agent, error) {
	agent, err := dl.FindAgent(ctx, bulker, dl.QueryAgentByAssessAPIKeyID, dl.FieldAccessAPIKeyID, id)
	if err != nil {
		if errors.Is(err, dl.ErrNotFound) {
			err = ErrAgentNotFound
		} else {
			err = fmt.Errorf("findAgentByApiKeyId: %w", err)
		}
	}
	return &agent, err
}

// parseMeta compares the agent and the request local_metadata content
// and returns fields to update the agent record or nil
func parseMeta(zlog zerolog.Logger, agent *model.Agent, req *checkin.CheckinRequest) ([]byte, error) {

	// Quick comparison first; compare the JSON payloads.
	// If the data is not consistently normalized, this short-circuit will not work.
	if bytes.Equal(req.LocalMeta, agent.LocalMetadata) {
		zlog.Trace().Msg("quick comparing local metadata is equal")
		return nil, nil
	}

	// Deserialize the request metadata
	var reqLocalMeta interface{}
	if err := json.Unmarshal(req.LocalMeta, &reqLocalMeta); err != nil {
		return nil, fmt.Errorf("parseMeta request: %w", err)
	}

	// If empty, don't step on existing data
	if reqLocalMeta == nil {
		return nil, nil
	}

	// Deserialize the agent's metadata copy
	var agentLocalMeta interface{}
	if err := json.Unmarshal(agent.LocalMetadata, &agentLocalMeta); err != nil {
		return nil, fmt.Errorf("parseMeta local: %w", err)
	}

	var outMeta []byte

	// Compare the deserialized meta structures and return the bytes to update if different
	if !reflect.DeepEqual(reqLocalMeta, agentLocalMeta) {

		zlog.Trace().
			RawJSON("oldLocalMeta", agent.LocalMetadata).
			RawJSON("newLocalMeta", req.LocalMeta).
			Msg("local metadata not equal")

		zlog.Info().
			RawJSON("req.LocalMeta", req.LocalMeta).
			Msg("applying new local metadata")

		outMeta = req.LocalMeta
	}

	return outMeta, nil
}

func parseComponents(zlog zerolog.Logger, agent *model.Agent, req *checkin.CheckinRequest) ([]byte, error) {

	// Quick comparison first; compare the JSON payloads.
	// If the data is not consistently normalized, this short-circuit will not work.
	if bytes.Equal(req.Components, agent.Components) {
		zlog.Trace().Msg("quick comparing agent components data is equal")
		return nil, nil
	}

	// Deserialize the request components data
	var reqComponents interface{}
	if len(req.Components) > 0 {
		if err := json.Unmarshal(req.Components, &reqComponents); err != nil {
			return nil, fmt.Errorf("parseComponents request: %w", err)
		}
		// Validate that components is an array
		if _, ok := reqComponents.([]interface{}); !ok {
			return nil, errors.New("parseComponets request: components property is not array")
		}
	}

	// If empty, don't step on existing data
	if reqComponents == nil {
		return nil, nil
	}

	// Deserialize the agent's components copy
	var agentComponents interface{}
	if len(agent.Components) > 0 {
		if err := json.Unmarshal(agent.Components, &agentComponents); err != nil {
			return nil, fmt.Errorf("parseComponents local: %w", err)
		}
	}

	var outComponents []byte

	// Compare the deserialized meta structures and return the bytes to update if different
	if !reflect.DeepEqual(reqComponents, agentComponents) {

		zlog.Trace().
			RawJSON("oldComponents", agent.Components).
			RawJSON("newComponents", req.Components).
			Msg("local components data is not equal")

		zlog.Info().
			RawJSON("req.Components", req.Components).
			Msg("applying new components data")

		outComponents = req.Components
	}

	return outComponents, nil
}
