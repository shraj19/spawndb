package commands

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/shraj19/flashdb/internal/resp"
	"github.com/shraj19/flashdb/internal/storage"
)

// PING command
func PING(args []string, client *storage.Client) string {
	return resp.PONG()
}

// ECHO command
func ECHO(args []string, client *storage.Client) string {
	if len(args) < 2 {
		return resp.Error("wrong number of arguments for 'echo'")
	}
	return resp.BulkString(args[1])
}

// INFO command
func INFO(args []string, client *storage.Client) string {
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

func WAIT(args []string, client *storage.Client) string {
	if len(args) < 3 {
		return resp.Error("wrong number of arguments for 'wait'")
	}

	replication := storage.EnsureReplication()

	numReplicas, err := strconv.Atoi(args[1])
	if err != nil {
		return resp.Error("invalid number of replicas")
	}
	timeoutMs, err := strconv.Atoi(args[2])
	if err != nil {
		return resp.Error("invalid timeout")
	}

	replication.Mu.RLock()
	targetOffset := replication.Offset
	replication.Mu.RUnlock()

	replication.Mu.RLock()
	connected := 0
	for _, r := range replication.Replicas {
		if r != nil && r.Connected {
			connected++
		}
	}
	replication.Mu.RUnlock()
	if connected == 0 {
		return resp.Integer(0)
	}

	if targetOffset == 0 {
		return resp.Integer(int64(connected))
	}

	replication.Mu.RLock()
	lastAsked := replication.LastAskedOffset
	replication.Mu.RUnlock()

	if targetOffset > lastAsked {
		time.Sleep(10 * time.Millisecond)
		if b, err := resp.EncodeCommand([]string{"REPLCONF", "GETACK", "*"}); err == nil {
			replication.Mu.RLock()
			conns := make([]*storage.ReplicaConnection, 0, len(replication.Replicas))
			for _, r := range replication.Replicas {
				if r != nil && r.Connected && r.Conn != nil {
					conns = append(conns, r)
				}
			}
			replication.Mu.RUnlock()
			for _, rc := range conns {
				if rc.Conn != nil {
					_, _ = rc.Conn.Write(b)
				}
			}
		}

		replication.Mu.Lock()
		replication.LastAskedOffset = targetOffset
		replication.Mu.Unlock()
	}

	deadline := time.Now().Add(time.Duration(timeoutMs) * time.Millisecond)
	for {
		acked := replication.CountReplicasAcked(targetOffset)
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
	replication := storage.EnsureReplication()
	replication.Mu.RLock()
	replid := replication.ReplID
	offset := replication.Offset
	connected := 0
	for _, r := range replication.Replicas {
		if r != nil && r.Connected {
			connected++
		}
	}
	replication.Mu.RUnlock()

	if replid == "" {
		replication.Mu.Lock()
		if replication.ReplID == "" {
			replication.ReplID = fmt.Sprintf("%x", time.Now().UnixNano())
		}
		replid = replication.ReplID
		replication.Mu.Unlock()
	}

	info := "# Replication\r\n"
	info += fmt.Sprintf("role:%s\r\n", replication.Role)
	info += fmt.Sprintf("connected_slaves:%d\r\n", connected)
	info += fmt.Sprintf("master_replid:%s\r\n", replid)
	info += fmt.Sprintf("master_repl_offset:%d\r\n", offset)
	return info
}

func REPLCONF(args []string, client *storage.Client) string {
	replication := storage.EnsureReplication()
	if client != nil && client.Conn != nil {
		addr := client.Conn.RemoteAddr().String()
		replication.Mu.Lock()
		rep, ok := replication.Replicas[addr]
		if !ok {
			rep = &storage.ReplicaConnection{Conn: client.Conn, Connected: true}
			replication.Replicas[addr] = rep
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
		replication.Mu.Unlock()
		if returnOK {
			return resp.OK()
		}
	}
	return ""
}

func PSYNC(args []string, client *storage.Client) string {
	replication := storage.EnsureReplication()
	replication.Mu.RLock()
	replid := replication.ReplID
	replication.Mu.RUnlock()
	if replid == "" {
		replication.Mu.Lock()
		if replication.ReplID == "" {
			replication.ReplID = fmt.Sprintf("%x", time.Now().UnixNano())
		}
		replid = replication.ReplID
		replication.Mu.Unlock()
	}

	if client != nil && client.Conn != nil {
		frame := fmt.Sprintf("+FULLRESYNC %s 0\r\n$0\r\n", replid)
		_, _ = client.Conn.Write([]byte(frame))
	}
	return ""
}