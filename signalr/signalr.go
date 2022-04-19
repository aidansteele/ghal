package signalr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/gorilla/websocket"
	"github.com/pkg/errors"
	"net/url"
)

type websocketMessage[T any] struct {
	Type      messageType `json:"type"`
	Target    string      `json:"target"`
	Arguments []T         `json:"arguments"`
}

type messageType int

const (
	MessageTypeUndefined messageType = iota
	MessageTypeInvocation
	MessageTypeStreamItem
	MessageTypeCompletion
	MessageTypeStreamInvocation
	MessageTypeCancelInvocation
	MessageTypePing
	MessageTypeClose
)

func Connect[T any](ctx context.Context, logsUrl, target string, ch chan<- T) error {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, logsUrl, nil)
	if err != nil {
		return errors.WithStack(err)
	}

	u, err := url.Parse(logsUrl)
	if err != nil {
		return errors.WithStack(err)
	}

	tenantId := u.Query().Get("tenantId")
	realRunId := u.Query().Get("runId")

	var jsonTerminator byte = 0x1E

	err = conn.WriteMessage(websocket.TextMessage, append([]byte(`{"protocol":"json","version":1}`), jsonTerminator))
	if err != nil {
		return errors.WithStack(err)
	}

	err = conn.WriteMessage(websocket.TextMessage, append([]byte(fmt.Sprintf(`{"arguments":["%s",%s],"target":"%s","type":1}`, tenantId, realRunId, target)), jsonTerminator))
	if err != nil {
		return errors.WithStack(err)
	}

	go func() {
		// we close the websocket conn after ctx cancellation,
		// otherwise we block forever on conn.ReadMessage()
		<-ctx.Done()
		conn.Close()
	}()

	for {
		_, wsMessage, err := conn.ReadMessage()
		if err != nil {
			return errors.WithStack(err)
		}

		split := bytes.Split(wsMessage, []byte{jsonTerminator})
		for _, part := range split {
			if len(part) == 0 || string(part) == "{}" {
				continue
			}

			message := websocketMessage[T]{}
			err = json.Unmarshal(part, &message)
			if err != nil {
				fmt.Println(err)
				return errors.WithStack(err)
			}

			switch message.Type {
			case MessageTypeInvocation:
				for _, argument := range message.Arguments {
					ch <- argument
				}
			case MessageTypeClose:
				conn.Close()
				return nil
			case MessageTypePing:
				// no-op
			default:
				//fmt.Printf("default %d: %s\n", message.Type, hex.EncodeToString(part))
				// no-op
			}
		}
	}
}
