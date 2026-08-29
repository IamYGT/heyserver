package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	xterm "github.com/charmbracelet/x/term"
	"github.com/gorilla/websocket"
)

const (
	terminalLocalEscape       = byte(0x1d) // Ctrl+]
	terminalPingInterval      = 15 * time.Second
	maxTerminalMessageBytes   = 256 << 10
	defaultTerminalColumns    = 80
	defaultTerminalRows       = 24
	terminalWebSocketProtocol = "hserver-terminal"
)

var errTerminalLocalEscape = errors.New("local terminal escape")

type cliTerminalMessage struct {
	Type     string `json:"type"`
	Data     string `json:"data,omitempty"`
	Encoding string `json:"encoding,omitempty"`
	Cols     uint16 `json:"cols,omitempty"`
	Rows     uint16 `json:"rows,omitempty"`
}

type cliTerminalSize struct {
	Cols uint16
	Rows uint16
}

func runTerminal(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("terminal", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	node := flags.String("node", "local", "managed node ID; default local host")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 0 {
		return errors.New("usage: hserverctl terminal [--node NODE]")
	}
	*node = strings.TrimSpace(*node)
	if *node == "" {
		return errors.New("terminal node must not be empty")
	}
	if len(*node) > 128 {
		return errors.New("terminal node must not exceed 128 bytes")
	}
	if client.token == "" {
		return errors.New("authentication token is not configured; run login or set HSERVER_TOKEN_FILE")
	}
	input := os.Stdin
	if !xterm.IsTerminal(input.Fd()) {
		return errors.New("terminal requires an interactive TTY on standard input")
	}
	cols, rows := terminalDimensions(input.Fd())
	previousState, err := xterm.MakeRaw(input.Fd())
	if err != nil {
		return fmt.Errorf("enable raw terminal input: %w", err)
	}
	defer xterm.Restore(input.Fd(), previousState) //nolint:errcheck

	resize := make(chan cliTerminalSize, 1)
	resizeSignals := make(chan os.Signal, 1)
	signal.Notify(resizeSignals, syscall.SIGWINCH)
	defer signal.Stop(resizeSignals)
	resizeCtx, cancelResize := context.WithCancel(ctx)
	defer cancelResize()
	go func() {
		for {
			select {
			case <-resizeCtx.Done():
				return
			case <-resizeSignals:
				width, height := terminalDimensions(input.Fd())
				select {
				case resize <- cliTerminalSize{Cols: width, Rows: height}:
				default:
					select {
					case <-resize:
					default:
					}
					resize <- cliTerminalSize{Cols: width, Rows: height}
				}
			}
		}
	}()

	return runTerminalSession(ctx, client, *node, cols, rows, input, out, resize)
}

func terminalDimensions(fd uintptr) (uint16, uint16) {
	width, height, err := xterm.GetSize(fd)
	if err != nil || width < 1 || height < 1 {
		return defaultTerminalColumns, defaultTerminalRows
	}
	if width > int(^uint16(0)) {
		width = int(^uint16(0))
	}
	if height > int(^uint16(0)) {
		height = int(^uint16(0))
	}
	return uint16(width), uint16(height)
}

func runTerminalSession(
	ctx context.Context,
	client *apiClient,
	node string,
	cols, rows uint16,
	input io.Reader,
	out io.Writer,
	resize <-chan cliTerminalSize,
) error {
	endpoint := terminalWebSocketURL(client.baseURL, node, cols, rows)
	dialer := *websocket.DefaultDialer
	dialer.HandshakeTimeout = client.httpClient.Timeout
	dialer.Subprotocols = []string{terminalWebSocketProtocol}
	header := make(http.Header)
	header.Set("Authorization", "Bearer "+client.token)
	connection, response, err := dialer.DialContext(ctx, endpoint.String(), header)
	if err != nil {
		if response != nil {
			defer response.Body.Close()
			body, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
			return httpStatusError(response.StatusCode, body)
		}
		return fmt.Errorf("connect terminal: %w", err)
	}
	defer connection.Close()
	connection.SetReadLimit(maxTerminalMessageBytes)
	if connection.Subprotocol() != terminalWebSocketProtocol {
		return errors.New("terminal server did not negotiate the hserver-terminal protocol")
	}

	sessionCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	inputMessages := make(chan cliTerminalMessage, 32)
	causes := make(chan error, 1)
	var reportOnce sync.Once
	report := func(cause error) {
		reportOnce.Do(func() {
			causes <- cause
			cancel()
		})
	}

	go func() {
		<-sessionCtx.Done()
		_ = connection.Close()
	}()
	go func() {
		if inputErr := pumpTerminalInput(sessionCtx, input, inputMessages); inputErr != nil {
			report(inputErr)
		}
	}()
	go func() {
		if writeErr := writeTerminalMessages(sessionCtx, connection, inputMessages, resize); writeErr != nil && sessionCtx.Err() == nil {
			report(writeErr)
		}
	}()

	for {
		var message cliTerminalMessage
		if err := connection.ReadJSON(&message); err != nil {
			select {
			case cause := <-causes:
				if errors.Is(cause, errTerminalLocalEscape) {
					return nil
				}
				return cause
			default:
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if websocket.IsCloseError(err, websocket.CloseNormalClosure) {
				return nil
			}
			return fmt.Errorf("read terminal: %w", err)
		}
		switch message.Type {
		case "ready", "pong":
			continue
		case "output":
			data := []byte(message.Data)
			if message.Encoding == "base64" {
				decoded, err := base64.StdEncoding.DecodeString(message.Data)
				if err != nil {
					return errors.New("terminal server returned invalid base64 output")
				}
				data = decoded
			}
			if _, err := out.Write(data); err != nil {
				return fmt.Errorf("write terminal output: %w", err)
			}
		case "error":
			if strings.TrimSpace(message.Data) == "" {
				return errors.New("terminal server reported an error")
			}
			return fmt.Errorf("terminal server: %s", message.Data)
		case "close":
			reason := strings.TrimSpace(message.Data)
			if reason == "" || reason == "process exited" {
				return nil
			}
			return fmt.Errorf("terminal closed: %s", reason)
		}
	}
}

func writeTerminalMessages(ctx context.Context, connection *websocket.Conn, input <-chan cliTerminalMessage, resize <-chan cliTerminalSize) error {
	ticker := time.NewTicker(terminalPingInterval)
	defer ticker.Stop()
	for {
		var message cliTerminalMessage
		select {
		case <-ctx.Done():
			return nil
		case message = <-input:
		case size := <-resize:
			message = cliTerminalMessage{Type: "resize", Cols: size.Cols, Rows: size.Rows}
		case <-ticker.C:
			message = cliTerminalMessage{Type: "ping"}
		}
		if err := connection.WriteJSON(message); err != nil {
			return fmt.Errorf("write terminal: %w", err)
		}
	}
}

func pumpTerminalInput(ctx context.Context, input io.Reader, messages chan<- cliTerminalMessage) error {
	buffer := make([]byte, 4096)
	for {
		count, err := input.Read(buffer)
		if count > 0 {
			data := buffer[:count]
			if escape := bytes.IndexByte(data, terminalLocalEscape); escape >= 0 {
				if escape > 0 {
					if err := queueTerminalInput(ctx, messages, data[:escape]); err != nil {
						return err
					}
				}
				return errTerminalLocalEscape
			}
			if err := queueTerminalInput(ctx, messages, data); err != nil {
				return err
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("read terminal input: %w", err)
		}
	}
}

func queueTerminalInput(ctx context.Context, messages chan<- cliTerminalMessage, data []byte) error {
	message := cliTerminalMessage{Type: "input", Data: string(data)}
	select {
	case <-ctx.Done():
		return nil
	case messages <- message:
		return nil
	}
}

func terminalWebSocketURL(base *url.URL, node string, cols, rows uint16) *url.URL {
	endpoint := *base
	if endpoint.Scheme == "https" {
		endpoint.Scheme = "wss"
	} else {
		endpoint.Scheme = "ws"
	}
	endpoint.Path = "/api/terminal/ws"
	endpoint.RawPath = ""
	endpoint.RawQuery = url.Values{
		"node":     []string{node},
		"cols":     []string{strconv.FormatUint(uint64(cols), 10)},
		"rows":     []string{strconv.FormatUint(uint64(rows), 10)},
		"encoding": []string{"base64"},
	}.Encode()
	return &endpoint
}
