package actor

import (
	"fmt"

	"github.com/ergo-services/ergo/etf"
	"github.com/ergo-services/ergo/gen"
)

func CreateDemoSup() gen.SupervisorBehavior {
	return &demoSup{}
}

type demoSup struct {
	gen.Supervisor
}

type demoSupServer struct {
	gen.Server
}

func (dss *demoSupServer) HandleCall(process *gen.ServerProcess, from gen.ServerFrom, message etf.Term) (etf.Term, gen.ServerStatus) {
	(*process).Parent().Behavior().(gen.Supervisor).StartChild(*process, "demoServer01", "01")
}

func (ds *demoSup) Init(args ...etf.Term) (gen.SupervisorSpec, error) {
	spec := gen.SupervisorSpec{
		Name: "demoAppSup",
		Children: []gen.SupervisorChildSpec{
			{
				Name:  "demoServer01",
				Child: NewAgent(),
				Args: []etf.Term{"01"},
			},
			// {
			// 	Name:  "demoServer02",
			// 	Child: NewAgent(),
			// 	Args: []etf.Term{"02"},
			// },
		},
		Strategy: gen.SupervisorStrategy{
			Type:      gen.SupervisorStrategyOneForAll,
			Intensity: 2,
			Period:    5,
			Restart:   gen.SupervisorStrategyRestartTemporary,
		},
	}
	return spec, nil
}

func (ds *demoSup) AddAgent(supervisor *gen.Process, id string) error { 
	// _, err := ds.StartChild(*supervisor, fmt.Sprintf("agent %s", a.id))
	_, err := ds.StartChild(*supervisor, "demoServer01", id)
	return err
}

type AddAgentMessage struct {
	Id string
}

type CountAgentMessage struct {}

func (ds *demoSup) HandleDirect(process *gen.Process, ref etf.Ref, message interface{}) (interface{}, gen.DirectStatus) { 
	println("direct")
	switch m := message.(type) {
		case AddAgentMessage:
			err := ds.AddAgent(process, m.Id)
			if err != nil {
				return "", fmt.Errorf("error during AddAgent: %s", err)
			}

			return nil, gen.DirectStatusOK
		case CountAgentMessage:
			pids, err := (*process).Children()
			if err != nil {
				return nil, fmt.Errorf("error during Children: %s", err)
			}

			return len(pids), gen.DirectStatusOK
	}

	(*process).Parent()

	return "", fmt.Errorf("unknown message type: %T", message)
}