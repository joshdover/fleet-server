package actor

import (
	"fmt"

	"github.com/elastic/fleet-server/v7/internal/pkg/checkin"
	"github.com/elastic/fleet-server/v7/internal/pkg/logger"
	"github.com/ergo-services/ergo/etf"
	"github.com/ergo-services/ergo/gen"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

type Agent struct {
	gen.Server
	zlog zerolog.Logger
	p *gen.ServerProcess
}

type controlS struct {
	id string
}

func NewAgent() gen.ServerBehavior {
	return &Agent{}
}

func (a *Agent) Init(sp *gen.ServerProcess, args ...etf.Term) error {
	aId := args[0].(string)

	fmt.Printf("Started new process\n\tPid: %s\n\tName: %q\n\tParent: %s\n\tArgs:%#v\n",
		sp.Self(),
		sp.Name(),
		sp.Parent().Self(),
		args)

	a.zlog = log.With().
		Str(logger.AgentID, aId).
		Str("process.id", sp.Self().String()).
		Logger()

	a.p = sp
	sp.State = &controlS{id: aId}

	return nil
}

func (a *Agent) GetId() (string, error) {
	return AgentGetId(a.p)
}

func AgentGetId(p *gen.ServerProcess) (string, error) {
	id, err := p.Call(p.Self(), GetIdMessage{})
	if err != nil {
		return "", err
	}

	return id.(string), nil
}

func (a *Agent) GetNextUpdate(msg GetNextUpdate) (*checkin.CheckinResponse, error) {
	return AgentGetNextUpdate(a.p, a.p.Self(), msg)
}

func AgentGetNextUpdate(p *gen.ServerProcess, to interface{}, msg GetNextUpdate) (*checkin.CheckinResponse, error) {
	res, err := p.Call(to, msg)
	if err != nil {
		return nil, err
	}

	return res.(*checkin.CheckinResponse), nil
}

type GetIdMessage struct{}

type GetNextUpdate struct {
	Ctx *checkin.CheckinContext
	Req *checkin.CheckinRequestExt
}

func (a *Agent) HandleCall(p *gen.ServerProcess, from gen.ServerFrom, message etf.Term) (etf.Term, gen.ServerStatus) {
	switch m := message.(type) {
	case GetIdMessage:
		return p.State.(*controlS).id, gen.ServerStatusOK
	case GetNextUpdate:
		res, err := checkin.GetNextUpdate(p.Context(), a.zlog, m.Ctx, m.Req)
		if err != nil {
			a.zlog.Error().Err(err).Msg("Error during GetNextUpdate")
			return nil, err
		}

		return res, gen.ServerStatusOK
	}

	return nil, fmt.Errorf("unknown message type: %T", message)
}

func (a *Agent) HandleDirect(process *gen.ServerProcess, ref etf.Ref, message interface{}) (interface{}, gen.DirectStatus) {
	switch message.(type) {
	case etf.Term:
		{
			reply, err := process.Behavior().(gen.ServerBehavior).HandleCall(process, gen.ServerFrom{Ref: ref}, message.(etf.Term))
			if err != nil {
				return "", fmt.Errorf("error during HandleCall: %s", err)
			}

			return reply, gen.DirectStatusOK
		}
	}

	return "", fmt.Errorf("unknown message type: %T", message)
}
