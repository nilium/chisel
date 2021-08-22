// chisel - A tool to fetch, transform, and serve data.
// Copyright (C) 2021 Noel Cower
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/hashicorp/go-sockaddr"
	"github.com/julienschmidt/httprouter"
	"github.com/rs/zerolog"
	"go.spiff.io/flagenv"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sys/unix"
)

func main() {
	zerolog.TimestampFunc = func() time.Time {
		return time.Now().UTC()
	}

	fs := flag.NewFlagSet("chisel", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	run := func() int {
		ctx, cancel := signal.NotifyContext(context.Background(), unix.SIGINT, unix.SIGTERM)
		defer cancel()
		return Main(ctx, fs, os.Args[1:])
	}
	os.Exit(run())
}

func Main(ctx context.Context, fs *flag.FlagSet, args []string) int {
	log := zerolog.New(fs.Output()).With().Timestamp().Logger()
	ctx = log.WithContext(ctx)

	var (
		configPath         = "config.json"
		printConfigAndExit bool
	)

	fs.StringVar(&configPath, "c", configPath, "The path to load program config JSON from.")
	fs.BoolVar(&printConfigAndExit, "C", printConfigAndExit, "Print the parsed program config and exit.")
	err := fs.Parse(args)
	if errors.Is(err, flag.ErrHelp) {
		return 2
	} else if err != nil {
		return 1
	}

	if err := flagenv.SetMissing(fs); err != nil {
		log.Error().Err(err).Msg("Error configuring chisel via environment.")
		return 1
	}

	conf, err := readConfigFile(configPath)
	if err != nil {
		log.Error().Err(err).Str("config", configPath).Msg("Failed to read config file.")
	}

	if printConfigAndExit {
		data, err := json.Marshal(conf)
		if err != nil {
			log.Error().Err(err).Msg("Failed to marshal program config.")
			return 1
		}
		log.Info().RawJSON("config", data).Msg("Config parsed, exiting.")
		return 0
	}

	if len(conf.Bind) == 0 {
		conf.Bind = []sockaddr.SockAddrMarshaler{
			sockaddr.SockAddrMarshaler{
				SockAddr: sockaddr.MustIPv4Addr("127.0.0.1:8080"),
			},
		}
	}

	listeners := make([]net.Listener, len(conf.Bind))
	servers := make([]*http.Server, len(conf.Bind))
	for i, caddr := range conf.Bind {
		bid := i + 1
		network, addr := caddr.ListenStreamArgs()
		llog := log.With().
			Int("binding", bid).
			Str("addr", addr).
			Str("net", network).
			Logger()
		switch t := caddr.Type(); t {
		case sockaddr.TypeUnix:
		case sockaddr.TypeIPv4, sockaddr.TypeIPv6:
		default:
			llog.Error().Stringer("type", t).Msg("Unrecognized binding type for address.")
			return 1
		}

		l, err := net.Listen(network, addr)
		if err != nil {
			llog.Error().Err(err).Msg("Failed to bind to address.")
			return 1
		}
		defer l.Close()

		rt := httprouter.New()
		for _, ed := range conf.Endpoints {
			if len(ed.Bind) > 0 && !ed.Bind.Contains(bid) {
				continue
			}
			handler := &Handler{EndpointDef: ed}
			method := strings.ToUpper(ed.Method)
			fn := handler.Get
			if method != "GET" {
				fn = handler.Post
			}
			rt.Handle(method, ed.Path, fn)
		}

		listeners[i] = l
		laddr := l.Addr().String()
		llog.Info().Stringer("laddr", l.Addr()).Msg("Listening on address.")

		log := log.With().
			Int("binding", bid).
			Str("laddr", laddr).
			Logger()

		ctx := log.WithContext(ctx)

		servers[i] = &http.Server{
			Handler: rt,
			BaseContext: func(net.Listener) context.Context {
				return ctx
			},
		}
	}

	wg, ctx := errgroup.WithContext(ctx)
	for i, sv := range servers {
		sv := sv
		l := listeners[i]
		laddr := l.Addr().String()

		log := log.With().
			Int("binding", i+1).
			Str("laddr", laddr).
			Logger()

		// Server.
		wg.Go(func() error {
			err := sv.Serve(l)
			if errors.Is(err, http.ErrServerClosed) {
				err = nil
			}
			return err
		})

		// Server shutdown.
		wg.Go(func() error {
			<-ctx.Done()
			log.Debug().Msg("Shutting down server.")
			closex, cancel := context.WithTimeout(context.Background(), time.Second*10)
			defer cancel()
			if err := sv.Shutdown(closex); err != nil {
				log.Warn().Err(err).Msg("Error closing server gracefully, forcing shutdown.")
			} else if err == nil {
				log.Info().Msg("Server closed.")
				return nil
			}
			if err := sv.Close(); err != nil {
				log.Error().Err(err).Msg("Error forcing server shutdown.")
			} else {
				log.Info().Msg("Server forced closed.")
			}
			return err
		})
	}

	if err := wg.Wait(); err != nil {
		log.Error().Err(err).Msg("Encountered fatal server error.")
		return 1
	}

	return 0
}

func readConfigFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("error reading config file: %w", err)
	}

	var conf *Config
	if err = json.Unmarshal(data, &conf); err != nil {
		return nil, fmt.Errorf("error parsing config file: %w", err)
	}

	return conf, nil
}
