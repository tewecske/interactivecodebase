// Package nats is a stub of github.com/nats-io/nats.go.
package nats

type Msg struct {
	Subject string
	Data    []byte
}

type MsgHandler func(msg *Msg)

type Subscription struct{ Subject string }

type Conn struct{ handlers map[string]MsgHandler }

func Connect(url string) (*Conn, error) { return &Conn{handlers: map[string]MsgHandler{}}, nil }

func (c *Conn) Subscribe(subj string, cb MsgHandler) (*Subscription, error) {
	c.handlers[subj] = cb
	return &Subscription{Subject: subj}, nil
}

func (c *Conn) QueueSubscribe(subj, queue string, cb MsgHandler) (*Subscription, error) {
	c.handlers[subj] = cb
	return &Subscription{Subject: subj}, nil
}
