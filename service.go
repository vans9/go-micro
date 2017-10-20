package micro

import (
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/micro/go-micro/client"
	"github.com/micro/go-micro/cmd"
	"github.com/micro/go-micro/metadata"
	"github.com/micro/go-micro/server"
)

type service struct {
	opts Options

	init chan bool
}

func newService(opts ...Option) Service {
	options := newOptions(opts...)

	options.Client = &clientWrapper{
		options.Client,
		metadata.Metadata{
			HeaderPrefix + "From-Service": options.Server.Options().Name,
		},
	}

	return &service{
		opts: options,
		init: make(chan bool),
	}
}

func (s *service) run(exit chan bool) {
	if s.opts.RegisterInterval <= time.Duration(0) {
		return
	}

	t := time.NewTicker(s.opts.RegisterInterval)

	for {
		select {
		case <-t.C:
			s.opts.Server.Register()
		case <-exit:
			t.Stop()
			return
		}
	}
}

// Init initialises options. Additionally it calls cmd.Init
// which parses command line flags. cmd.Init is only called
// on first Init.
func (s *service) Init(opts ...Option) {
	// If <-s.init blocks, Init has not been called yet
	// so we can call cmd.Init once.
	select {
	case <-s.init:
		// only process options
		for _, o := range opts {
			o(&s.opts)
		}
	default:
		// close init
		close(s.init)

		// process options
		for _, o := range opts {
			o(&s.opts)
		}

		// Initialise the command flags, overriding new service
		s.opts.Cmd.Init(
			cmd.Broker(&s.opts.Broker),
			cmd.Registry(&s.opts.Registry),
			cmd.Transport(&s.opts.Transport),
			cmd.Client(&s.opts.Client),
			cmd.Server(&s.opts.Server),
		)
	}
}

func (s *service) Options() Options {
	return s.opts
}

func (s *service) Client() client.Client {
	return s.opts.Client
}

func (s *service) Server() server.Server {
	return s.opts.Server
}

func (s *service) String() string {
	return "go-micro"
}

func (s *service) Start() error {
	for _, fn := range s.opts.BeforeStart {
		if err := fn(); err != nil {
			return err
		}
	}

	if err := s.opts.Server.Start(); err != nil {
		return err
	}

	if err := s.opts.Server.Register(); err != nil {
		return err
	}

	for _, fn := range s.opts.AfterStart {
		if err := fn(); err != nil {
			return err
		}
	}

	return nil
}

func (s *service) Stop() error {
	var gerr error

	for _, fn := range s.opts.BeforeStop {
		if err := fn(); err != nil {
			gerr = err
		}
	}

	if err := s.opts.Server.Deregister(); err != nil {
		return err
	}

	if err := s.opts.Server.Stop(); err != nil {
		return err
	}

	for _, fn := range s.opts.AfterStop {
		if err := fn(); err != nil {
			gerr = err
		}
	}

	return gerr
}

func (s *service) Run() error {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGTERM, syscall.SIGINT, syscall.SIGKILL)
	return s.runUntilSignal(ch)
}

func (s *service) runUntilSignal(ch chan os.Signal) error {
	var err error
	exit := make(chan bool)
	result := make(chan error)

	go s.RunUntilChannelClose(exit, result)

	select {
	// wait on kill signal
	case <-ch:
	// wait on error
	case errorResult := <-result:
		err = errorResult
	}

	close(exit)
	return err
}

func (s *service) RunUntilChannelClose(exit chan bool, errChan chan error) {
	if err := s.Start(); err != nil {
		errChan <- err
		return
	}

	// start reg loop
	ex := make(chan bool)
	go s.run(ex)

	select {
	// wait on kill signal
	case <-exit:
	// wait on context cancel
	case <-s.opts.Context.Done():
	}

	// exit reg loop
	close(ex)

	if err := s.Stop(); err != nil {
		errChan <- err
		return
	}

	errChan <- nil
	return
}
