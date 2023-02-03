package actor

import (
	"fmt"

	"github.com/ergo-services/ergo/etf"
	"github.com/ergo-services/ergo/gen"
)

type agentSup struct {
	gen.Server
}

type controlState struct {
	sup        *supAgents
	supProcess gen.Process
	agents map[string]etf.Pid
}

func CreateAgentSupervisor() *agentSup {
	return &agentSup{}
}

func (c *agentSup) Init(process *gen.ServerProcess, args ...etf.Term) error {

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
		agents: make(map[string]etf.Pid),
	}

	process.State = state
	return nil
}

type AddAgentMessage struct {
	Id string
}

type RemoveAgentMessage struct {
	Id string
}

type CountAgentMessage struct{}

func (c *agentSup) HandleCall(p *gen.ServerProcess, from gen.ServerFrom, message etf.Term) (etf.Term, gen.ServerStatus) {
	fmt.Printf("agentController.HandleCall: %v, %v", from, message)
	state := p.State.(*controlState)

	switch m := message.(type) {
	// Add an agent
	case AddAgentMessage:
		child, err := state.sup.StartChild(state.supProcess, "agent", m.Id)
		if err != nil {
			return nil, err
		}
		// state.agents = append(state.agents, child.Self())
		state.agents[m.Id] = child.Self()

		// monitor process in order to restart it on termination
		p.MonitorProcess(child.Self())

		return nil, gen.DirectStatusOK

	// Remove an agent
	case RemoveAgentMessage:
		child := state.agents[m.Id]
		childP := p.ProcessByPid(child)
		if childP == nil {
			return nil, fmt.Errorf("agent process not found")
		}
		if err := childP.Exit("remove agent"); err != nil {
			return nil, fmt.Errorf("error during agent process exit: %s", err)
		}

		delete(state.agents, m.Id)

		return nil, gen.DirectStatusOK

	// Get count of agents
	case CountAgentMessage:
		return len(state.agents), gen.DirectStatusOK
	}

	return nil, fmt.Errorf("unknown message type")
}

func (c *agentSup) HandleDirect(p *gen.ServerProcess, ref etf.Ref, message interface{}) (interface{}, gen.DirectStatus) {
	// Forward to call
	fmt.Printf("agentController.HandleDirect: %v, %v", ref, message)
	return c.HandleCall(p, gen.ServerFrom{Ref: ref}, message)
}

func (c *agentSup) HandleInfo(process *gen.ServerProcess, message etf.Term) gen.ServerStatus {
	state := process.State.(*controlState)
	switch m := message.(type) {
	case gen.MessageDown:
		for id, pid := range state.agents {
			if pid != m.Pid {
				continue
			}
			// restart terminated agent
			child, err := state.sup.StartChild(state.supProcess, "agent", id)
			if err != nil {
				return err
			}
			process.MonitorProcess(child.Self())
			state.agents[id] = child.Self()
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
			{
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
