package commands

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/shraj19/flashdb/internal/core"
	"github.com/shraj19/flashdb/internal/replication"
	"github.com/shraj19/flashdb/internal/resp"
)

// PING command
func PING(args []string, client *core.Client) string {
	return resp.PONG()
}

// ECHO command
func ECHO(args []string, client *core.Client) string {
	if len(args) < 2 {
		return resp.Error("wrong number of arguments for 'echo'")
	}
	return resp.BulkString(args[1])
}

// INFO command
func INFO(args []string, client *core.Client) string {
	section := "default" // section is default if not specified
	if len(args) > 1 {
		section = args[1]
	}

	var info string

	switch section {
	case "default", "server", "all":
		info = buildServerInfo()
	case "replication":
		info = buildReplicationInfo()
	default:
		return resp.Error(fmt.Sprintf("Unknown section '%s'", section))
	}
	
	return resp.BulkString(info)
}

func buildServerInfo() string {
	info := "# Server\r\n"
	info += "redis_version:0.0.1\r\n"
	info += "tcp_port:6379\r\n"
	return info
}

func WAIT(args []string, client *core.Client) string {
	if len(args) < 3 {
		return resp.Error("wrong number of arguments for 'wait'")
	}

	repl := replication.EnsureState()

	numReplicas, err := strconv.Atoi(args[1])
	if err != nil {
		return resp.Error("invalid number of replicas")
	}
	timeoutMs, err := strconv.Atoi(args[2])
	if err != nil {
		return resp.Error("invalid timeout")
	}

	repl.Mu.RLock()
	targetOffset := repl.Offset
	repl.Mu.RUnlock()

	repl.Mu.RLock()
	connected := 0
	for _, r := range repl.Replicas {
		if r != nil && r.Connected {
			connected++
		}
	}
	repl.Mu.RUnlock()
	if connected == 0 {
		return resp.Integer(0)
	}

	if targetOffset == 0 {
		return resp.Integer(int64(connected))
	}

	repl.Mu.RLock()
	lastAsked := repl.LastAskedOffset
	repl.Mu.RUnlock()

	if targetOffset > lastAsked {
		time.Sleep(10 * time.Millisecond)
		if b, err := resp.EncodeCommand([]string{"REPLCONF", "GETACK", "*"}); err == nil {
			repl.Mu.RLock()
			conns := make([]*replication.ReplicaConnection, 0, len(repl.Replicas))
			for _, r := range repl.Replicas {
				if r != nil && r.Connected && r.Conn != nil {
					conns = append(conns, r)
				}
			}
			repl.Mu.RUnlock()
			for _, rc := range conns {
				if rc.Conn != nil {
					_, _ = rc.Conn.Write(b)
				}
			}
		}

		repl.Mu.Lock()
		repl.LastAskedOffset = targetOffset
		repl.Mu.Unlock()
	}

	deadline := time.Now().Add(time.Duration(timeoutMs) * time.Millisecond)
	for {
		acked := repl.CountReplicasAcked(targetOffset)
		if acked >= numReplicas {
			return resp.Integer(int64(acked))
		}
		if time.Now().After(deadline) {
			return resp.Integer(int64(acked))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func buildReplicationInfo() string {
	repl := replication.EnsureState()
	repl.Mu.RLock()
	replid := repl.ReplID
	offset := repl.Offset
	connected := 0
	for _, r := range repl.Replicas {
		if r != nil && r.Connected {
			connected++
		}
	}
	repl.Mu.RUnlock()

	if replid == "" {
		repl.Mu.Lock()
		if repl.ReplID == "" {
			repl.ReplID = fmt.Sprintf("%x", time.Now().UnixNano())
		}
		replid = repl.ReplID
		repl.Mu.Unlock()
	}

	info := "# Replication\r\n"
	info += fmt.Sprintf("role:%s\r\n", repl.Role)
	info += fmt.Sprintf("connected_slaves:%d\r\n", connected)
	info += fmt.Sprintf("master_replid:%s\r\n", replid)
	info += fmt.Sprintf("master_repl_offset:%d\r\n", offset)
	return info
}

func REPLCONF(args []string, client *core.Client) string {
	repl := replication.EnsureState()
	if client != nil && client.Conn != nil {
		addr := client.Conn.RemoteAddr().String()
		repl.Mu.Lock()
		rep, ok := repl.Replicas[addr]
		if !ok {
			rep = &replication.ReplicaConnection{Conn: client.Conn, Connected: true}
			repl.Replicas[addr] = rep
		} else {
			rep.Conn = client.Conn
			rep.Connected = true
		}
		returnOK := true
		if len(args) >= 2 {
			sub := strings.ToLower(args[1])
			switch sub {
			case "listening-port":
				if len(args) >= 3 {
					rep.ListeningPort = args[2]
				}
			case "ack":
				if len(args) >= 3 {
					if v, err := strconv.ParseInt(args[2], 10, 64); err == nil {
						rep.AckOffset = v
					}
				}
				returnOK = false
			}
		}
		repl.Mu.Unlock()
		if returnOK {
			return resp.OK()
		}
	}
	return ""
}

func PSYNC(args []string, client *core.Client) string {
	repl := replication.EnsureState()
	repl.Mu.RLock()
	replid := repl.ReplID
	repl.Mu.RUnlock()
	if replid == "" {
		repl.Mu.Lock()
		if repl.ReplID == "" {
			repl.ReplID = fmt.Sprintf("%x", time.Now().UnixNano())
		}
		replid = repl.ReplID
		repl.Mu.Unlock()
	}

	if client != nil && client.Conn != nil {
		frame := fmt.Sprintf("+FULLRESYNC %s 0\r\n$0\r\n", replid)
		_, _ = client.Conn.Write([]byte(frame))
	}
	return ""
}
