package actor

import (
	"fmt"

	"github.com/ergo-services/ergo/etf"
	"github.com/ergo-services/ergo/gen"
)

const GET_ID = etf.Atom("get_id")

type Agent struct {
	gen.Server
	id string
}

func NewAgent() gen.ServerBehavior {
	return &Agent{}
}

func (a *Agent) Init(sp *gen.ServerProcess, args ...etf.Term) error {
	a.id = args[0].(string)

	fmt.Printf("Started new process\n\tPid: %s\n\tName: %q\n\tParent: %s\n\tArgs:%#v\n",
		sp.Self(),
		sp.Name(),
		sp.Parent().Self(),
		args)

	return nil
}

func (a *Agent) GetId(process *gen.ServerProcess) (string, error) {
	id, err := process.Call(process.Self(), GET_ID)
	if err != nil {
		return "", err
	}

	return id.(string), nil
}

func (a *Agent) HandleCall(process *gen.ServerProcess, from gen.ServerFrom, message etf.Term) (etf.Term, gen.ServerStatus) {
	switch message.(etf.Atom) {
	case GET_ID:
		return a.id, gen.ServerStatusOK
	}
	return nil, gen.ServerStatusOK
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
