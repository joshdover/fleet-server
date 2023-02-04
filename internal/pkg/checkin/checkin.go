package checkin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"time"

	"github.com/elastic/fleet-server/v7/internal/pkg/action"
	"github.com/elastic/fleet-server/v7/internal/pkg/bulk"
	"github.com/elastic/fleet-server/v7/internal/pkg/config"
	"github.com/elastic/fleet-server/v7/internal/pkg/dl"
	"github.com/elastic/fleet-server/v7/internal/pkg/logger"
	"github.com/elastic/fleet-server/v7/internal/pkg/model"
	"github.com/elastic/fleet-server/v7/internal/pkg/monitor"
	"github.com/elastic/fleet-server/v7/internal/pkg/policy"
	"github.com/elastic/fleet-server/v7/internal/pkg/smap"
	"github.com/elastic/fleet-server/v7/internal/pkg/sqn"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

const (
	TypePolicyChange  = "POLICY_CHANGE"
	TypeUnenroll      = "UNENROLL"
	TypeUpgrade       = "UPGRADE"
	TypeUpdateTags    = "UPDATE_TAGS"
	TypeForceUnenroll = "FORCE_UNENROLL"
)

var (
	ErrNoPolicyOutput = errors.New("output section not found")
)

type CheckinContext struct {
	Ad     *action.Dispatcher
	Pm     policy.Monitor
	Bc     *Bulk
	Bulker bulk.Bulk
	Gcp    monitor.GlobalCheckpointProvider
	Cfg    *config.Server
}

type CheckinRequest struct {
	Status     string          `json:"status"`
	Message    string          `json:"message"`
	AckToken   string          `json:"ack_token,omitempty"`
	LocalMeta  json.RawMessage `json:"local_metadata"`
	Components json.RawMessage `json:"components,omitempty"`
}

type CheckinRequestExt struct {
	CheckinRequest
	Agent         *model.Agent
	Start         time.Time
	Ver           string
	RawMeta       []byte
	RawComponents []byte
	Seqno         sqn.SeqNo
}

type CheckinResponse struct {
	AckToken string       `json:"ack_token,omitempty"`
	Action   string       `json:"action"`
	Actions  []ActionResp `json:"actions,omitempty"`
}

type ActionResp struct {
	AgentID    string      `json:"agent_id"`
	CreatedAt  string      `json:"created_at"`
	StartTime  string      `json:"start_time,omitempty"`
	Expiration string      `json:"expiration,omitempty"`
	Data       interface{} `json:"data"`
	ID         string      `json:"id"`
	Type       string      `json:"type"`
	InputType  string      `json:"input_type"`
	Timeout    int64       `json:"timeout,omitempty"`
}

func NewCheckinRequestExt(req *CheckinRequest, agent *model.Agent, start time.Time, ver string, rawMeta []byte, rawComponents []byte, seqno sqn.SeqNo) *CheckinRequestExt {
	return &CheckinRequestExt{
		CheckinRequest: *req,
		Agent:          agent,
		Start:          start,
		Ver:            ver,
		RawMeta:        rawMeta,
		RawComponents:  rawComponents,
		Seqno:          seqno,
	}
}

func GetNextUpdate(ctx context.Context, zlog zerolog.Logger, cCtx *CheckinContext, req *CheckinRequestExt) (*CheckinResponse, error) {
	zlog.Debug().Msgf("checkin request: %+v", req)
	// Subscribe to actions dispatcher
	aSub := cCtx.Ad.Subscribe(req.Agent.Id, req.Seqno)
	defer cCtx.Ad.Unsubscribe(aSub)
	actCh := aSub.Ch()

	// Subscribe to policy manager for changes on PolicyId > policyRev
	sub, err := cCtx.Pm.Subscribe(req.Agent.Id, req.Agent.PolicyID, req.Agent.PolicyRevisionIdx, req.Agent.PolicyCoordinatorIdx)
	if err != nil {
		return nil, fmt.Errorf("subscribe policy monitor: %w", err)
	}
	defer func() {
		err := cCtx.Pm.Unsubscribe(sub)
		if err != nil {
			zlog.Error().Err(err).Str("policy_id", req.Agent.PolicyID).Msg("unable to unsubscribe from policy")
		}
	}()

	// Update check-in timestamp on timeout
	tick := time.NewTicker(cCtx.Cfg.Timeouts.CheckinTimestamp)
	defer tick.Stop()

	setupDuration := time.Since(req.Start)
	pollDuration, jitter := calcPollDuration(zlog, cCtx.Cfg, setupDuration)

	zlog.Debug().
		Str("status", req.Status).
		Str("seqNo", req.Seqno.String()).
		Dur("setupDuration", setupDuration).
		Dur("jitter", jitter).
		Dur("pollDuration", pollDuration).
		// Uint64("bodyCount", readCounter.Count()).
		Msg("checkin start long poll")

	// Chill out for a bit. Long poll.
	longPoll := time.NewTicker(pollDuration)
	defer longPoll.Stop()

	// Initial update on checkin, and any user fields that might have changed
	err = cCtx.Bc.CheckIn(req.Agent.Id, req.Status, req.Message, req.RawMeta, req.RawComponents, req.Seqno, req.Ver)
	if err != nil {
		zlog.Error().Err(err).Str("agent_id", req.Agent.Id).Msg("checkin failed")
	}

	// Initial fetch for pending actions
	var (
		actions  []ActionResp
		ackToken string
	)

	// Check agent pending actions first
	pendingActions, err := fetchAgentPendingActions(ctx, cCtx, req)
	if err != nil {
		return nil, err
	}
	pendingActions = filterActions(req.Agent.Id, pendingActions)
	actions, ackToken = convertActions(req.Agent.Id, pendingActions)

	if len(actions) == 0 {
	LOOP:
		for {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case acdocs := <-actCh:
				var acs []ActionResp
				acdocs = filterActions(req.Agent.Id, acdocs)
				acs, ackToken = convertActions(req.Agent.Id, acdocs)
				actions = append(actions, acs...)
				break LOOP
			case policy := <-sub.Output():
				actionResp, err := processPolicy(ctx, zlog, cCtx.Bulker, req.Agent.Id, policy)
				if err != nil {
					return nil, fmt.Errorf("processPolicy: %w", err)
				}
				actions = append(actions, *actionResp)
				break LOOP
			case <-longPoll.C:
				zlog.Trace().Msg("fire long poll")
				break LOOP
			case <-tick.C:
				err := cCtx.Bc.CheckIn(req.Agent.Id, req.Status, req.Message, nil, req.RawComponents, nil, req.Ver)
				if err != nil {
					zlog.Error().Err(err).Str("agent_id", req.Agent.Id).Msg("checkin failed")
				}
			}
		}
	}

	for _, action := range actions {
		zlog.Info().
			Str("ackToken", ackToken).
			Str("createdAt", action.CreatedAt).
			Str("id", action.ID).
			Str("type", action.Type).
			Str("inputType", action.InputType).
			Int64("timeout", action.Timeout).
			Msg("Action delivered to agent on checkin")
	}

	resp := &CheckinResponse{
		AckToken: ackToken,
		Action:   "checkin",
		Actions:  actions,
	}

	return resp, nil
}

func fetchAgentPendingActions(ctx context.Context, cCtx *CheckinContext, req *CheckinRequestExt) ([]model.Action, error) {

	actions, err := dl.FindAgentActions(ctx, cCtx.Bulker, req.Seqno, cCtx.Gcp.GetCheckpoint(), req.Agent.Id)

	if err != nil {
		return nil, fmt.Errorf("fetchAgentPendingActions: %w", err)
	}

	return actions, err
}

// filterActions removes the POLICY_CHANGE, UPDATE_TAGS, FORCE_UNENROLL action from the passed list.
// The source of this list are documents from the fleet actions index.
// The POLICY_CHANGE action that the agent receives are generated by the fleet-server when it detects a different policy in processRequest()
// The UPDATE_TAGS, FORCE_UNENROLL actions are UI only actions, should not be delivered to agents
func filterActions(agentID string, actions []model.Action) []model.Action {
	resp := make([]model.Action, 0, len(actions))
	for _, action := range actions {
		ignoredTypes := map[string]bool{
			TypePolicyChange:  true,
			TypeUpdateTags:    true,
			TypeForceUnenroll: true,
		}
		if exists := ignoredTypes[action.Type]; exists {
			log.Info().Str("agent_id", agentID).Str("action_id", action.ActionID).Str("type", action.Type).Msg("Removing action found in index from check in response")
			continue
		}
		resp = append(resp, action)
	}
	return resp

}

func convertActions(agentID string, actions []model.Action) ([]ActionResp, string) {
	var ackToken string
	sz := len(actions)

	respList := make([]ActionResp, 0, sz)
	for _, action := range actions {
		respList = append(respList, ActionResp{
			AgentID:    agentID,
			CreatedAt:  action.Timestamp,
			StartTime:  action.StartTime,
			Expiration: action.Expiration,
			Data:       action.Data,
			ID:         action.ActionID,
			Type:       action.Type,
			InputType:  action.InputType,
			Timeout:    action.Timeout,
		})
	}

	if sz > 0 {
		ackToken = actions[sz-1].Id
	}

	return respList, ackToken
}

// A new policy exists for this agent.  Perform the following:
//   - Generate and update default ApiKey if roles have changed.
//   - Rewrite the policy for delivery to the agent injecting the key material.
func processPolicy(ctx context.Context, zlog zerolog.Logger, bulker bulk.Bulk, agentID string, pp *policy.ParsedPolicy) (*ActionResp, error) {
	zlog = zlog.With().
		Str("fleet.ctx", "processPolicy").
		Int64("fleet.policyRevision", pp.Policy.RevisionIdx).
		Int64("fleet.policyCoordinator", pp.Policy.CoordinatorIdx).
		Str(logger.PolicyID, pp.Policy.PolicyID).
		Logger()

	// Repull and decode the agent object. Do not trust the cache.
	agent, err := dl.FindAgent(ctx, bulker, dl.QueryAgentByID, dl.FieldID, agentID)
	if err != nil {
		zlog.Error().Err(err).Msg("fail find agent record")
		return nil, err
	}

	// Parse the outputs maps in order to prepare the outputs
	const outputsProperty = "outputs"
	outputs, err := smap.Parse(pp.Fields[outputsProperty])
	if err != nil {
		return nil, err
	}

	if outputs == nil {
		return nil, ErrNoPolicyOutput
	}

	// Iterate through the policy outputs and prepare them
	for _, policyOutput := range pp.Outputs {
		err = policyOutput.Prepare(ctx, zlog, bulker, &agent, outputs)
		if err != nil {
			return nil, fmt.Errorf("failed to prepare output %q:: %w",
				policyOutput.Name, err)
		}
	}

	outputRaw, err := json.Marshal(outputs)
	if err != nil {
		return nil, err
	}

	// Dupe field map; pp is immutable
	fields := make(map[string]json.RawMessage, len(pp.Fields))

	for k, v := range pp.Fields {
		fields[k] = v
	}

	// Update only the output fields to avoid duping the whole map
	fields[outputsProperty] = json.RawMessage(outputRaw)

	rewrittenPolicy := struct {
		Policy map[string]json.RawMessage `json:"policy"`
	}{fields}

	r := policy.RevisionFromPolicy(pp.Policy)
	resp := ActionResp{
		AgentID:   agent.Id,
		CreatedAt: pp.Policy.Timestamp,
		Data:      rewrittenPolicy,
		ID:        r.String(),
		Type:      TypePolicyChange,
	}

	return &resp, nil
}

func calcPollDuration(zlog zerolog.Logger, cfg *config.Server, setupDuration time.Duration) (time.Duration, time.Duration) {

	pollDuration := cfg.Timeouts.CheckinLongPoll

	// Under heavy load, elastic may take along time to authorize the api key, many seconds to minutes.
	// Short circuit the long poll to take the setup delay into account.  This is particularly necessary
	// in cloud where the proxy will time us out after 5m20s causing unnecessary errors.

	if setupDuration >= pollDuration {
		// We took so long to setup that we need to exit immediately
		pollDuration = time.Millisecond
		zlog.Warn().
			Dur("setupDuration", setupDuration).
			Dur("pollDuration", cfg.Timeouts.CheckinLongPoll).
			Msg("excessive setup duration short cicuit long poll")

	} else {
		pollDuration -= setupDuration
		if setupDuration > (time.Second * 10) {
			zlog.Warn().
				Dur("setupDuration", setupDuration).
				Dur("pollDuration", pollDuration).
				Msg("checking poll duration decreased due to slow setup")
		}
	}

	var jitter time.Duration
	if cfg.Timeouts.CheckinJitter != 0 {
		jitter = time.Duration(rand.Int63n(int64(cfg.Timeouts.CheckinJitter))) //nolint:gosec // jitter time does not need to by generated from a crypto secure source
		if jitter < pollDuration {
			pollDuration = pollDuration - jitter
			zlog.Trace().Dur("poll", pollDuration).Msg("Long poll with jitter")
		}
	}

	return pollDuration, jitter
}
