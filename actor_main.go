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

	// a := actor.NewAgent("foo")
	// p, err := node.Spawn("example_server", gen.ProcessOptions{}, a)
	// if err != nil {
	// 	panic(err)
	// }

	demoSup := actor.CreateDemoSup()
	sup, err := node.Spawn("demoSup", gen.ProcessOptions{}, demoSup)
	if err != nil {
		panic(err)
	}
	fmt.Println("Started supervisor process", sup.Self())

	http.HandleFunc("/", func (w http.ResponseWriter, r *http.Request) {
		// TODO this doesn't work yet because we need a GenServer to receive messages
		// to receive comamnds to create children. Seems like supervisor should delegate to behavior?
		_, err := sup.Direct(actor.AddAgentMessage{Id: "123"})
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintf(w, "direct error: %v", err.Error())
			return
		}

		// fmt.Fprintf(w, "Hello, %q", r.URL.Path)
		// cnt, err := sup.Direct(actor.CountAgentMessage{})
		// if err != nil {
		// 	panic(err.Error())
		// }

		// fmt.Fprintf(w, "Reply from the agent: %v", cnt)
	})

	go http.ListenAndServe(":8220", nil)

	node.Wait()
}
