// Copyright Elasticsearch B.V. and/or licensed to Elasticsearch B.V. under one
// or more contributor license agreements. Licensed under the Elastic License;
// you may not use this file except in compliance with the Elastic License.

package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/format"
	"io/ioutil"
	"os"
	"text/template"

	"github.com/elastic/elastic-agent/pkg/packer"
	"github.com/elastic/fleet-server/v7/licenses"
)

var (
	input   string
	output  string
	license string
)

func init() {
	flag.StringVar(&input, "in", "", "Source of input. \"-\" means reading from stdin")
	flag.StringVar(&output, "out", "-", "Output path. \"-\" means writing to stdout")
	flag.StringVar(&license, "license", "Elastic", "License header for generated file.")
}

var tmpl = template.Must(template.New("specs").Parse(`
{{ .License }}
// Code generated by dev-tools/cmd/buildlimits/buildlimits.go - DO NOT EDIT.

package config

import (
	"math"
	"runtime"
	"strings"
	"time"

	"github.com/elastic/elastic-agent/pkg/packer"
	"github.com/elastic/go-ucfg/yaml"
	"github.com/pbnjay/memory"
	"github.com/pkg/errors"
)

const (
	defaultCacheNumCounters = 500000           // 10x times expected count
	defaultCacheMaxCost     = 50 * 1024 * 1024 // 50MiB cache size

	defaultMaxConnections = 0 // no limit
	defaultPolicyThrottle = time.Millisecond * 5

	defaultCheckinInterval = time.Millisecond
	defaultCheckinBurst    = 1000
	defaultCheckinMax      = 0
	defaultCheckinMaxBody  = 1024 * 1024

	defaultArtifactInterval = time.Millisecond * 5
	defaultArtifactBurst    = 25
	defaultArtifactMax      = 50
	defaultArtifactMaxBody  = 0

	defaultEnrollInterval = time.Millisecond * 10
	defaultEnrollBurst    = 100
	defaultEnrollMax      = 50
	defaultEnrollMaxBody  = 1024 * 512

	defaultAckInterval = time.Millisecond * 10
	defaultAckBurst    = 100
	defaultAckMax      = 50
	defaultAckMaxBody  = 1024 * 1024 * 2

	defaultStatusInterval = time.Millisecond * 5
	defaultStatusBurst    = 25
	defaultStatusMax      = 50
	defaultStatusMaxBody  = 0
)

type valueRange struct {
	Min int ` + "`config:\"min\"`" + `
	Max int ` + "`config:\"max\"`" + `
}

type envLimits struct {
	RAM    valueRange           ` + "`config:\"ram\"`" + `
	Server *serverLimitDefaults ` + "`config:\"server_limits\"`" + `
	Cache  *cacheLimits         ` + "`config:\"cache_limits\"`" + `
}

func defaultEnvLimits() *envLimits {
	return &envLimits{
		RAM: valueRange{
			Min: 0,
			Max: int(getMaxInt()),
		},
		Server: defaultserverLimitDefaults(),
		Cache:  defaultCacheLimits(),
	}
}

type cacheLimits struct {
	NumCounters int64 ` + "`config:\"num_counters\"`" + `
	MaxCost     int64 ` + "`config:\"max_cost\"`" + `
}

func defaultCacheLimits() *cacheLimits {
	return &cacheLimits{
		NumCounters: defaultCacheNumCounters,
		MaxCost:     defaultCacheMaxCost,
	}
}

type limit struct {
	Interval time.Duration ` + "`config:\"interval\"`" + `
	Burst    int           ` + "`config:\"burst\"`" + `
	Max      int64         ` + "`config:\"max\"`" + `
	MaxBody  int64         ` + "`config:\"max_body_byte_size\"`" + `
}

type serverLimitDefaults struct {
	PolicyThrottle time.Duration ` + "`config:\"policy_throttle\"`" + `
	MaxConnections int           ` + "`config:\"max_connections\"`" + `

	CheckinLimit  limit ` + "`config:\"checkin_limit\"`" + `
	ArtifactLimit limit ` + "`config:\"artifact_limit\"`" + `
	EnrollLimit   limit ` + "`config:\"enroll_limit\"`" + `
	AckLimit      limit ` + "`config:\"ack_limit\"`" + `
	StatusLimit   limit ` + "`config:\"status_limit\"`" + `
}

func defaultserverLimitDefaults() *serverLimitDefaults {
	return &serverLimitDefaults{
		PolicyThrottle: defaultCacheNumCounters,
		MaxConnections: defaultCacheMaxCost,

		CheckinLimit: limit{
			Interval: defaultCheckinInterval,
			Burst:    defaultCheckinBurst,
			Max:      defaultCheckinMax,
			MaxBody:  defaultCheckinMaxBody,
		},
		ArtifactLimit: limit{
			Interval: defaultArtifactInterval,
			Burst:    defaultArtifactBurst,
			Max:      defaultArtifactMax,
			MaxBody:  defaultArtifactMaxBody,
		},
		EnrollLimit: limit{
			Interval: defaultEnrollInterval,
			Burst:    defaultEnrollBurst,
			Max:      defaultEnrollMax,
			MaxBody:  defaultEnrollMaxBody,
		},
		AckLimit: limit{
			Interval: defaultAckInterval,
			Burst:    defaultAckBurst,
			Max:      defaultAckMax,
			MaxBody:  defaultAckMaxBody,
		},
		StatusLimit: limit{
			Interval: defaultStatusInterval,
			Burst:    defaultStatusBurst,
			Max:      defaultStatusMax,
			MaxBody:  defaultStatusMaxBody,
		},
	}
}

var defaults []*envLimits

func init() {
	// Packed Files
	{{ range $i, $f := .Files -}}
	// {{ $f }}
	{{ end -}}
	unpacked := packer.MustUnpack("{{ .Pack }}")

	for f, v := range unpacked {
		cfg, err := yaml.NewConfig(v, DefaultOptions...)
		if err != nil {
			panic(errors.Wrap(err, "Cannot read spec from "+f))
		}

		l := defaultEnvLimits()
		if err := cfg.Unpack(&l, DefaultOptions...); err != nil {
			panic(errors.Wrap(err, "Cannot unpack spec from "+f))
		}

		defaults = append(defaults, l)
	}
}

func loadLimits() *envLimits {
	ramSize := int(memory.TotalMemory() / 1024 / 1024)
	return loadLimitsForRam(ramSize)
}

func loadLimitsForRam(currentRAM int) *envLimits {
	for _, l := range defaults {
		// get max possible config for current env
		if l.RAM.Min < currentRAM && currentRAM <= l.RAM.Max {
			return l
		}
	}

	return defaultEnvLimits()
}

func getMaxInt() int64 {
	if strings.HasSuffix(runtime.GOARCH, "64") {
		return math.MaxInt64
	}
	return math.MaxInt32
}

`))

func main() {
	flag.Parse()

	if len(input) == 0 {
		fmt.Fprintln(os.Stderr, "Invalid input source")
		os.Exit(1)
	}

	l, err := licenses.Find(license)
	if err != nil {
		fmt.Fprintf(os.Stderr, "problem to retrieve the license, error: %+v", err)
		os.Exit(1)
		return
	}

	data, err := gen(input, l)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error while generating the file, err: %+v\n", err)
		os.Exit(1)
	}

	if output == "-" {
		_, _ = os.Stdout.Write(data)
		return
	} else {
		_, _ = ioutil.WriteFile(output, data, 0600)
	}

	return
}

func gen(_ string, l string) ([]byte, error) {
	pack, files, err := packer.Pack(input)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	_ = tmpl.Execute(&buf, struct {
		Pack    string
		Files   []string
		License string
	}{
		Pack:    pack,
		Files:   files,
		License: l,
	})

	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		return nil, err
	}

	return formatted, nil
}
