package replication

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/shraj19/flashdb/internal/core"
	"github.com/shraj19/flashdb/internal/resp"
)

func StartReplica(masterHost, masterPort, localPort string) {
	addr := net.JoinHostPort(masterHost, masterPort)
	backoff := 200 * time.Millisecond
	for {
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			fmt.Println("replica: failed to connect to master:", err)
			time.Sleep(backoff)
			if backoff < 2*time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = 200 * time.Millisecond

		fmt.Println("replica: connected to master", addr)
		State.Mu.Lock()
		State.Role = "slave"
		State.Mu.Unlock()
		reader := bufio.NewReader(conn)
		var localOffset int64 = 0

		if b, err := resp.EncodeCommand([]string{"PING"}); err == nil {
			_, _ = conn.Write(b)
			_, _ = reader.ReadString('\n')
		}

		if b, err := resp.EncodeCommand([]string{"REPLCONF", "listening-port", localPort}); err == nil {
			_, _ = conn.Write(b)
			if line, err := reader.ReadString('\n'); err == nil && strings.TrimSpace(line) != "+OK" {
				conn.Close()
				return
			}
		}

		if b, err := resp.EncodeCommand([]string{"REPLCONF", "capa", "psync2"}); err == nil {
			_, _ = conn.Write(b)
			if line, err := reader.ReadString('\n'); err == nil && strings.TrimSpace(line) != "+OK" {
				conn.Close()
				return
			}
		}

		if b, err := resp.EncodeCommand([]string{"PSYNC", "?", "-1"}); err == nil {
			_, _ = conn.Write(b)
			line, err := reader.ReadString('\n')
			if err != nil {
				conn.Close()
				return
			}
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "+FULLRESYNC") {
				lenLine, err := reader.ReadString('\n')
				if err != nil {
					conn.Close()
					return
				}
				if strings.HasPrefix(strings.TrimSpace(lenLine), "$") {
					var rdbLen int
					fmt.Sscanf(strings.TrimSpace(lenLine)[1:], "%d", &rdbLen)
					if rdbLen > 0 {
						buf := make([]byte, rdbLen)
						if _, err := io.ReadFull(reader, buf); err != nil {
							conn.Close()
							return
						}
					}
				}
			}

			for {
				args, err := resp.ParseRESP(reader)
				if err != nil {
					if err == io.EOF {
						conn.Close()
						break
					}
					fmt.Println("replica: parse error:", err)
					conn.Close()
					break
				}
				if len(args) == 0 {
					continue
				}

				if strings.ToUpper(args[0]) == "REPLCONF" && len(args) >= 2 && strings.ToUpper(args[1]) == "GETACK" {
					enc, err := resp.EncodeCommand(args)
					encLen := 0
					if err == nil {
						encLen = len(enc)
					}
					if b, err := resp.EncodeCommand([]string{"REPLCONF", "ACK", fmt.Sprintf("%d", localOffset)}); err == nil {
						_, _ = conn.Write(b)
					}
					localOffset += int64(encLen)
					continue
				}

				if handler, ok := core.CommandMap[strings.ToUpper(args[0])]; ok {
					dummyClient := &core.Client{Conn: nil}
					handler(args, dummyClient)
					if enc, err := resp.EncodeCommand(args); err == nil {
						localOffset += int64(len(enc))
					}
				}
			}
		}

		time.Sleep(200 * time.Millisecond)
	}
}
