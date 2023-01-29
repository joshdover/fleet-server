package actor

import (
	"fmt"

	"github.com/ergo-services/ergo/etf"
	"github.com/ergo-services/ergo/gen"
)

type agentController struct {
	gen.Server
}

type controlState struct {
	sup        *supAgents
	supProcess gen.Process
	agents     []etf.Pid
}

func CreateAgentController() *agentController {
	return &agentController{}
}

func (c *agentController) Init(process *gen.ServerProcess, args ...etf.Term) error {

	sup := &supAgents{}
	supOptions := gen.ProcessOptions{
		Context: process.Context(), // inherited context to make sup process be hard linked with the control one
	}
	supProcess, err := process.Spawn("", supOptions, sup)
	if err != nil {
		return fmt.Errorf("error during subProcess Spawn: %s", err)
	}
	state := &controlState{
		sup:        sup,
		supProcess: supProcess,
	}

	process.State = state
	return nil
}

type AddAgentMessage struct {
	Id string
}

type CountAgentMessage struct{}

func (c *agentController) HandleCall(p *gen.ServerProcess, from gen.ServerFrom, message etf.Term) (etf.Term, gen.ServerStatus) {
	fmt.Printf("agentController.HandleCall: %v, %v", from, message)
	state := p.State.(*controlState)

	switch m := message.(type) {
	// Add an agent
	case AddAgentMessage:
		child, err := state.sup.StartChild(state.supProcess, "agent", m.Id)
		if err != nil {
			return nil, err
		}
		state.agents = append(state.agents, child.Self())

		// monitor process in order to restart it on termination
		p.MonitorProcess(child.Self())

		return nil, gen.DirectStatusOK
	// Get count of agents
	case CountAgentMessage:
		return len(state.agents), gen.DirectStatusOK
	}

	return nil, fmt.Errorf("unknown message type")
}

func (c *agentController) HandleDirect(p *gen.ServerProcess, ref etf.Ref, message interface{}) (interface{}, gen.DirectStatus) {
	// Forward to call
	fmt.Printf("agentController.HandleDirect: %v, %v", ref, message)
	return c.HandleCall(p, gen.ServerFrom{Ref: ref}, message)
}

func (c *agentController) HandleInfo(process *gen.ServerProcess, message etf.Term) gen.ServerStatus {
	state := process.State.(*controlState)
	switch m := message.(type) {
	case gen.MessageDown:
		for i, pid := range state.agents {
			if pid != m.Pid {
				continue
			}
			// restart terminated agent
			child, err := state.sup.StartChild(state.supProcess, "agent")
			if err != nil {
				return err
			}
			process.MonitorProcess(child.Self())
			state.agents[i] = child.Self()
			break
		}
	default:
		// TODO: hard failure?
		fmt.Printf("unknown request: %#v\n", m)
	}

	return gen.ServerStatusOK
}

type supAgents struct {
	gen.Supervisor
}

func (s *supAgents) Init(args ...etf.Term) (gen.SupervisorSpec, error) {
	return gen.SupervisorSpec{
		Name: "sup_agents",
		Children: []gen.SupervisorChildSpec{
			gen.SupervisorChildSpec{
				Name:  "agent",
				Child: &Agent{},
			},
		},
		Strategy: gen.SupervisorStrategy{
			Type:      gen.SupervisorStrategySimpleOneForOne,
			Intensity: 5,
			Period:    5,
			Restart:   gen.SupervisorStrategyRestartTemporary,
		},
	}, nil
}
