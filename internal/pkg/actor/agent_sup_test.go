package actor

import (
	"testing"

	"github.com/ergo-services/ergo"
	"github.com/ergo-services/ergo/gen"
	"github.com/ergo-services/ergo/node"
)

func TestAgentSup(t *testing.T) {
	// Create a new node
	node, err := ergo.StartNode("test@127.0.0.1", "cookie", node.Options{})
	if err != nil {
		t.Errorf("Error starting node: %v", err)
	}

	agentController := CreateAgentSupervisor()
	sup, err := node.Spawn("AgentSupervisor", gen.ProcessOptions{}, agentController)
	if err != nil {
		t.Errorf("Error spawning AgentSupervisor: %v", err)
	}

	msgCnt := 0
	direct := func(message interface{}) (interface{}) {
		ret, err := sup.Direct(message)
		if err != nil {
			t.Fatalf("Error sending message %v: %v", msgCnt, err)
		}

		return ret
	}

	expectEq := func (a interface{}, b interface{}) {
		if a != b {
			t.Fatalf("Expected %v, got %v", b, a)
		}
	}

	t.Run("add and remove agent", func(t *testing.T) {
		cnt := direct(CountAgentMessage{})
		expectEq(cnt, 0)
		direct(AddAgentMessage{Id: "1"})
	
		cnt = direct(CountAgentMessage{})
		expectEq(cnt, 1)

		direct(RemoveAgentMessage{Id: "1"})
	
		cnt = direct(CountAgentMessage{})
		expectEq(cnt, 0)
	})

	node.Stop()
}
