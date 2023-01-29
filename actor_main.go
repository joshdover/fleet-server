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

	a := actor.NewAgent("foo")
	p, err := node.Spawn("example_server", gen.ProcessOptions{}, a)
	if err != nil {
		panic(err)
	}

	http.HandleFunc("/", func (w http.ResponseWriter, r *http.Request) {
		id, err := p.Direct(actor.GET_ID)
		if err != nil {
			println("error: ", err.Error())
		} else {
			println("id: ", id.(string))
		}

		fmt.Fprintf(w, "Reply from the agent: %s", id.(string))
	})

	go http.ListenAndServe(":8220", nil)

	node.Wait()
}
