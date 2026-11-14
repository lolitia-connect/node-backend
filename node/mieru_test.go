package node

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/enfein/mieru/v3/pkg/appctl/appctlcommon"
	"github.com/enfein/mieru/v3/pkg/appctl/appctlpb"
	"github.com/enfein/mieru/v3/pkg/protocol"
	"github.com/enfein/mieru/v3/pkg/socks5"
	"github.com/perfect-panel/ppanel-node/api/panel"
	"github.com/perfect-panel/ppanel-node/conf"
	"google.golang.org/protobuf/proto"
)

func TestMieruTransportsRevokeUsersAndRestartWithoutPanel(t *testing.T) {
	for _, transport := range []string{"tcp", "udp"} {
		t.Run(transport, func(t *testing.T) {
			var empty, unavailable atomic.Bool
			var onlineReports atomic.Int32
			const uuid = "00000000-0000-0000-0000-000000000017"
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.URL.Query().Get("protocol_id") != "2" {
					t.Error("missing mieru protocol instance ID")
				}
				if unavailable.Load() {
					http.Error(writer, "offline", http.StatusServiceUnavailable)
					return
				}
				if request.URL.Path == "/v1/server/online" {
					var body panel.UserOnlineBody
					if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					if len(body.Users) != 1 || body.Users[0] != (panel.OnlineUser{UID: 17, IP: "127.0.0.1"}) {
						t.Errorf("mieru %s online users = %+v", transport, body.Users)
					}
					onlineReports.Add(1)
					fmt.Fprint(writer, `{"code":200}`)
					return
				}
				users := []panel.UserInfo{{Id: 17, Uuid: uuid}}
				if empty.Load() {
					users = []panel.UserInfo{}
				}
				_ = json.NewEncoder(writer).Encode(map[string]any{"code": 200, "data": panel.UserListBody{Users: users}})
			}))
			defer server.Close()
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			port := listener.Addr().(*net.TCPAddr).Port
			_ = listener.Close()
			client, err := panel.NewNodeClient(&conf.NodeApiConfig{
				APIHost: server.URL, NodeType: "mieru", ProtocolID: "2", Timeout: 1,
			})
			if err != nil {
				t.Fatal(err)
			}
			controller := NewMieruController(conf.New(), client, &panel.NodeInfo{
				Type: "mieru", PullInterval: 3600, PushInterval: 3600,
				Protocol: &panel.Protocol{ID: "2", Type: "mieru", Port: port, Transport: transport},
			})
			if err := controller.Start(); err != nil {
				t.Fatal(err)
			}
			defer controller.Close()
			connect := func() (net.Conn, error) {
				t.Helper()
				binding := &appctlpb.PortBinding{Port: proto.Int32(int32(port)), Protocol: appctlpb.TransportProtocol_TCP.Enum()}
				if transport == "udp" {
					binding.Protocol = appctlpb.TransportProtocol_UDP.Enum()
				}
				mux, err := appctlcommon.NewClientMuxFromProfile(&appctlpb.ClientProfile{
					ProfileName: proto.String("test"),
					User:        &appctlpb.User{Name: proto.String(uuid), Password: proto.String(uuid)},
					Servers: []*appctlpb.ServerEndpoint{{
						IpAddress: proto.String("127.0.0.1"), PortBindings: []*appctlpb.PortBinding{binding},
					}},
				}, nil, nil, nil, nil)
				if err != nil {
					return nil, err
				}
				t.Cleanup(func() { _ = mux.Close() })
				ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
				defer cancel()
				connection, err := mux.DialContext(ctx)
				if err != nil {
					return nil, err
				}
				_ = connection.SetDeadline(time.Now().Add(3 * time.Second))
				return connection, nil
			}
			request := []byte{5, 1, 0, 1, 127, 0, 0, 1, 0, 80}
			connection, err := connect()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := connection.Write(request); err != nil {
				t.Fatal(err)
			}
			reply := make([]byte, 10)
			if _, err := io.ReadFull(connection, reply); err != nil {
				t.Fatal(err)
			}
			if reply[0] != 5 || reply[1] != 2 {
				t.Fatalf("authenticated proxy reply = %v, want loopback rejection", reply)
			}
			if err := controller.reportOnline(t.Context()); err != nil {
				t.Fatal(err)
			}
			if onlineReports.Load() != 1 {
				t.Fatal("authenticated mieru session was not reported online")
			}
			_ = connection.Close()
			controller.collectTraffic(0)
			if len(controller.traffic.pending) != 1 {
				t.Fatal("authenticated mieru traffic was not queued")
			}
			empty.Store(true)
			if err := controller.userListMonitor(t.Context()); err != nil {
				t.Fatal(err)
			}
			connection, err = connect()
			if err == nil {
				_, _ = connection.Write(request)
				_, err = io.ReadFull(connection, reply)
				_ = connection.Close()
			}
			if err == nil {
				t.Fatal("revoked mieru user authenticated")
			}
			if err := controller.Stop(); err != nil {
				t.Fatal(err)
			}
			unavailable.Store(true)
			if err := controller.Start(); err != nil {
				t.Fatalf("restart with cached empty users: %v", err)
			}
			unavailable.Store(false)
			empty.Store(false)
			if err := controller.userListMonitor(t.Context()); err != nil {
				t.Fatal(err)
			}
			connection, err = connect()
			if err != nil {
				t.Fatal(err)
			}
			defer connection.Close()
			_, _ = connection.Write(request)
			if _, err := io.ReadFull(connection, reply); err != nil {
				t.Fatalf("restored mieru user: %v", err)
			}
		})
	}
}

func TestMieruTrafficAccumulatesThresholdAndRetries(t *testing.T) {
	var fail atomic.Bool
	fail.Store(true)
	var uploaded atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if fail.Load() {
			http.Error(writer, "retry", http.StatusServiceUnavailable)
			return
		}
		var report panel.ServerPushUserTrafficRequest
		if err := json.NewDecoder(request.Body).Decode(&report); err != nil {
			t.Error(err)
			return
		}
		for _, traffic := range report.Traffic {
			uploaded.Add(traffic.Upload)
		}
		fmt.Fprint(writer, `{"code":200}`)
	}))
	defer server.Close()
	client, err := panel.NewNodeClient(&conf.NodeApiConfig{APIHost: server.URL, NodeType: "mieru"})
	if err != nil {
		t.Fatal(err)
	}
	controller := NewMieruController(conf.New(), client, &panel.NodeInfo{Type: "mieru"})
	controller.pendingTraffic[17] = panel.UserTraffic{UID: 17, Upload: 60}
	controller.collectTraffic(100)
	if len(controller.traffic.pending) != 0 {
		t.Fatal("below-threshold traffic was queued")
	}
	traffic := controller.pendingTraffic[17]
	traffic.Upload += 60
	controller.pendingTraffic[17] = traffic
	controller.collectTraffic(100)
	if err := controller.traffic.report(t.Context()); err == nil {
		t.Fatal("failed report was acknowledged")
	}
	next := &Node{}
	next.InheritTraffic(&Node{traffic: controller.traffic})
	fail.Store(false)
	if err := next.FlushTraffic(t.Context()); err != nil {
		t.Fatal(err)
	}
	if uploaded.Load() != 120 || len(controller.traffic.pending) != 0 {
		t.Fatalf("uploaded = %d, pending = %d", uploaded.Load(), len(controller.traffic.pending))
	}
}

type mieruTestUserConn struct {
	net.Conn
}

func (connection *mieruTestUserConn) UserName() string {
	return "echo-user"
}

func (connection *mieruTestUserConn) RemoteAddr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP("::ffff:192.0.2.17"), Port: 12345}
}

func TestMieruOnlineReportsRetryShortAndActiveSessions(t *testing.T) {
	var fail atomic.Bool
	fail.Store(true)
	var reports atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/server/online" || request.URL.Query().Get("protocol_id") != "2" {
			t.Errorf("unexpected online request: %s", request.URL)
		}
		var body panel.UserOnlineBody
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if len(body.Users) != 1 || body.Users[0] != (panel.OnlineUser{UID: 17, IP: "192.0.2.17"}) {
			t.Errorf("online users = %+v", body.Users)
		}
		reports.Add(1)
		if fail.Load() {
			http.Error(writer, "retry", http.StatusServiceUnavailable)
			return
		}
		fmt.Fprint(writer, `{"code":200}`)
	}))
	defer server.Close()
	client, err := panel.NewNodeClient(&conf.NodeApiConfig{
		APIHost: server.URL, NodeType: "mieru", ProtocolID: "2",
	})
	if err != nil {
		t.Fatal(err)
	}
	controller := NewMieruController(conf.New(), client, &panel.NodeInfo{Type: "mieru"})
	controller.running = true
	controller.updateUserIDs([]panel.UserInfo{{Id: 17, Uuid: "echo-user"}})
	connection := &mieruConn{Conn: &mieruTestUserConn{}, controller: controller, users: controller.userIDs}
	controller.connections[connection] = struct{}{}
	connection.record(1, 0)
	if err := controller.reportOnline(t.Context()); err == nil {
		t.Fatal("failed online report was acknowledged")
	}
	delete(controller.connections, connection)
	fail.Store(false)
	if err := controller.reportOnline(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := controller.reportOnline(t.Context()); err != nil {
		t.Fatal(err)
	}
	if reports.Load() != 2 {
		t.Fatalf("short-session reports = %d, want one failure and one retry", reports.Load())
	}
	connection = &mieruConn{Conn: &mieruTestUserConn{}, controller: controller, users: controller.userIDs}
	controller.connections[connection] = struct{}{}
	connection.record(0, 1)
	for range 2 {
		if err := controller.reportOnline(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if reports.Load() != 4 {
		t.Fatalf("active-session reports = %d, want repeated heartbeat without traffic", reports.Load())
	}
	delete(controller.connections, connection)
	if err := controller.reportOnline(t.Context()); err != nil {
		t.Fatal(err)
	}
	if reports.Load() != 4 {
		t.Fatal("closed acknowledged session was renewed")
	}
}

func TestMieruOnlineReportBatchesAndPreservesNewSessions(t *testing.T) {
	var reports, total atomic.Int32
	var controller *MieruController
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body panel.UserOnlineBody
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if len(body.Users) == 0 || len(body.Users) > 10000 {
			t.Errorf("invalid online batch size: %d", len(body.Users))
		}
		total.Add(int32(len(body.Users)))
		if reports.Add(1) == 1 {
			controller.mu.Lock()
			for _, user := range body.Users {
				controller.onlineSequence++
				controller.pendingOnline[user] = controller.onlineSequence
			}
			controller.mu.Unlock()
		}
		fmt.Fprint(writer, `{"code":200}`)
	}))
	defer server.Close()
	client, err := panel.NewNodeClient(&conf.NodeApiConfig{APIHost: server.URL, NodeType: "mieru"})
	if err != nil {
		t.Fatal(err)
	}
	controller = NewMieruController(conf.New(), client, &panel.NodeInfo{Type: "mieru"})
	for index := range 10001 {
		controller.onlineSequence++
		controller.pendingOnline[panel.OnlineUser{UID: 17, IP: fmt.Sprintf("10.0.%d.%d", index/256, index%256)}] = controller.onlineSequence
	}
	if err := controller.reportOnline(t.Context()); err != nil {
		t.Fatal(err)
	}
	if reports.Load() != 2 || total.Load() != 10001 || len(controller.pendingOnline) != 10000 {
		t.Fatalf("reports = %d, total = %d, pending = %d", reports.Load(), total.Load(), len(controller.pendingOnline))
	}
	if err := controller.reportOnline(t.Context()); err != nil {
		t.Fatal(err)
	}
	if reports.Load() != 3 || total.Load() != 20001 || len(controller.pendingOnline) != 0 {
		t.Fatalf("retry reports = %d, total = %d, pending = %d", reports.Load(), total.Load(), len(controller.pendingOnline))
	}
}

func TestMieruOnlineReportsExcludeRevokedUsers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		t.Error("revoked session was reported online")
		fmt.Fprint(writer, `{"code":200}`)
	}))
	defer server.Close()
	client, err := panel.NewNodeClient(&conf.NodeApiConfig{APIHost: server.URL, NodeType: "mieru"})
	if err != nil {
		t.Fatal(err)
	}
	controller := NewMieruController(conf.New(), client, &panel.NodeInfo{Type: "mieru"})
	controller.running = true
	controller.updateUserIDs([]panel.UserInfo{{Id: 17, Uuid: "echo-user"}})
	connection := &mieruConn{Conn: &mieruTestUserConn{}, controller: controller, users: controller.userIDs}
	controller.connections[connection] = struct{}{}
	connection.record(1, 0)
	controller.updateUserIDs(nil)
	if err := controller.reportOnline(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestMieruSessionForwardsAndCountsBothDirections(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	echoDone := make(chan struct{})
	go func() {
		defer close(echoDone)
		connection, err := listener.Accept()
		if err != nil {
			return
		}
		defer connection.Close()
		_, _ = io.Copy(connection, connection)
	}()
	controller := NewMieruController(conf.New(), &panel.NodeClient{}, &panel.NodeInfo{Type: "mieru"})
	controller.running = true
	controller.updateUserIDs([]panel.UserInfo{{Id: 17, Uuid: "echo-user"}})
	client, incoming := net.Pipe()
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(3 * time.Second))
	server, err := socks5.New(&socks5.Config{
		AuthOpts: socks5.Auth{ClientSideAuthentication: true}, AllowLoopbackDestination: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	served := make(chan error, 1)
	go func() {
		served <- server.ServeConn(&mieruConn{
			Conn: &mieruTestUserConn{Conn: incoming}, controller: controller, users: controller.userIDs,
		})
	}()
	port := listener.Addr().(*net.TCPAddr).Port
	request := []byte{5, 1, 0, 1, 127, 0, 0, 1, byte(port >> 8), byte(port)}
	if _, err := client.Write(request); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 10)
	if _, err := io.ReadFull(client, reply); err != nil || reply[1] != 0 {
		t.Fatalf("SOCKS5 reply = %v, error = %v", reply, err)
	}
	payload := []byte("mieru forwarding regression")
	if _, err := client.Write(payload); err != nil {
		t.Fatal(err)
	}
	echo := make([]byte, len(payload))
	if _, err := io.ReadFull(client, echo); err != nil || string(echo) != string(payload) {
		t.Fatalf("echo = %q, error = %v", echo, err)
	}
	_ = client.Close()
	select {
	case <-served:
	case <-time.After(3 * time.Second):
		t.Fatal("mieru relay did not stop after client disconnect")
	}
	<-echoDone
	controller.collectTraffic(0)
	for _, batch := range controller.traffic.pending {
		traffic := batch.users[17]
		if traffic.Upload != int64(len(request)+len(payload)) || traffic.Download != int64(len(reply)+len(payload)) {
			t.Fatalf("counted traffic = %+v", traffic)
		}
		return
	}
	t.Fatal("mieru relay traffic was not queued")
}

func TestBuildMieruUsersPreservesCredentialsAndSkipsEmptyUUID(t *testing.T) {
	users := buildMieruUsers([]panel.UserInfo{{Id: 1}, {Id: 2, Uuid: "credential"}})
	if len(users) != 1 || users["credential"].GetName() != "credential" || users["credential"].GetPassword() != "credential" {
		t.Fatalf("unexpected mieru credentials: %v", users)
	}
	registry := protocol.NewMux(false).SetServerUsers(users)
	registry.SetServerUsers(buildMieruUsers(nil))
	_ = registry.Close()
}
