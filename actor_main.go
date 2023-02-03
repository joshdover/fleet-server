package main

import (
	"fmt"
	"net/http"

	"github.com/elastic/fleet-server/v7/internal/pkg/actor"
	"github.com/ergo-services/ergo"
	"github.com/ergo-services/ergo/gen"
	"github.com/ergo-services/ergo/node"
)

type A struct {
	a string
}

func main() {
	node, err := ergo.StartNode("demo@127.0.0.1", "cookie", node.Options{})
	if err != nil {
		panic(err)
	}

	agentController := actor.CreateAgentSupervisor()
	sup, err := node.Spawn("AgentSupervisor", gen.ProcessOptions{}, agentController)
	if err != nil {
		panic(err)
	}
	fmt.Println("Started control process", sup.Self())

	http.HandleFunc("/add", func(w http.ResponseWriter, r *http.Request) {
		cnt, err := sup.Direct(actor.CountAgentMessage{})
		if err != nil {
			panic(err.Error())
		}

		_, err = sup.Direct(actor.AddAgentMessage{Id: string(cnt.(int))})
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintf(w, "direct error: %v", err.Error())
			return
		}

		cnt, err = sup.Direct(actor.CountAgentMessage{})
		if err != nil {
			panic(err.Error())
		}

		fmt.Fprintf(w, "Reply from the agent: %v", cnt)
	})

	http.HandleFunc("/remove", func(w http.ResponseWriter, r *http.Request) {
		cnt, err := sup.Direct(actor.CountAgentMessage{})
		if err != nil {
			panic(err.Error())
		}

		_, err = sup.Direct(actor.AddAgentMessage{Id: string(cnt.(int))})
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintf(w, "direct error: %v", err.Error())
			return
		}

		cnt, err = sup.Direct(actor.CountAgentMessage{})
		if err != nil {
			panic(err.Error())
		}

		fmt.Fprintf(w, "Reply from the agent: %v", cnt)
	})

	go http.ListenAndServe(":8220", nil)

	node.Wait()
}
