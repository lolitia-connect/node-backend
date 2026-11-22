package node

import (
	"errors"
	"fmt"
	"reflect"

	"github.com/perfect-panel/ppanel-node/api/panel"
)

type protocolKey struct {
	typ string
	id  string
}

func protocolIdentity(protocol *panel.Protocol) protocolKey {
	id := protocol.ID
	if id == "" {
		id = protocol.Type
	}
	return protocolKey{typ: protocol.Type, id: id}
}

type protocolService interface {
	Prepare() error
	Start() error
	Stop() error
}

type protocolInstance struct {
	info    *panel.NodeInfo
	service protocolService
}

func (n *Node) protocols() []protocolInstance {
	instances := make([]protocolInstance, 0, len(n.controllers)+len(n.mieruNodes))
	for _, c := range n.controllers {
		instances = append(instances, protocolInstance{c.info, c})
	}
	for _, c := range n.mieruNodes {
		instances = append(instances, protocolInstance{c.info, c})
	}
	return instances
}

func (p protocolInstance) needsReplacement(next protocolInstance) bool {
	if c, ok := p.service.(*Controller); ok && c.certificateReload.Load() {
		return true
	}
	return !reflect.DeepEqual(runtimeProtocol(*p.info.Protocol), runtimeProtocol(*next.info.Protocol))
}

func runtimeProtocol(p panel.Protocol) panel.Protocol {
	// Display and accounting metadata do not change the inbound.
	p.Name = ""
	p.Ratio = 0
	if len(p.ALPN) == 0 {
		p.ALPN = nil
	}
	return p
}

// Reload switches only the affected protocols on the same core. Callers
// serialize it with Start and Stop.
func (n *Node) Reload(next *Node) (bool, error) {
	next.InheritTraffic(n)
	previous := n.protocols()
	merged, toStart, toStop := planProtocolReload(previous, next.protocols())
	for _, p := range toStart {
		key := protocolIdentity(p.info.Protocol)
		if err := p.service.Prepare(); err != nil {
			return false, fmt.Errorf("prepare %s(%s): %w", key.typ, key.id, err)
		}
	}

	changed := len(toStop)+len(toStart) != 0
	if changed {
		// The reporter reads both the controller lists and their running state.
		n.stopReporter()
		defer n.startReporter()
		var stopped, attempted []protocolInstance
		rollback := func(cause error) (bool, error) {
			result := cause
			for _, p := range attempted {
				result = errors.Join(result, runProtocol("stop replacement", p, p.service.Stop))
			}
			for _, p := range stopped {
				result = errors.Join(result, runProtocol("restore", p, p.service.Start))
			}
			return false, result
		}
		// Stop the affected listeners together so they can exchange ports.
		for _, p := range toStop {
			if err := runProtocol("stop", p, p.service.Stop); err != nil {
				return rollback(err)
			}
			stopped = append(stopped, p)
		}
		for _, p := range toStart {
			attempted = append(attempted, p)
			if err := runProtocol("start", p, p.service.Start); err != nil {
				return rollback(err)
			}
		}
		n.controllers = n.controllers[:0]
		n.mieruNodes = n.mieruNodes[:0]
		for _, p := range merged {
			switch c := p.service.(type) {
			case *Controller:
				n.controllers = append(n.controllers, c)
			case *MieruController:
				n.mieruNodes = append(n.mieruNodes, c)
			}
		}
	}
	return changed, nil
}

func planProtocolReload(previous, next []protocolInstance) (merged []protocolInstance, toStart, toStop []protocolInstance) {
	oldByKey := make(map[protocolKey]protocolInstance, len(previous))
	for _, p := range previous {
		oldByKey[protocolIdentity(p.info.Protocol)] = p
	}
	merged = append([]protocolInstance(nil), next...)
	retained := make(map[protocolKey]bool, len(merged))
	for i, p := range merged {
		key := protocolIdentity(p.info.Protocol)
		if old, ok := oldByKey[key]; ok && old.info.Protocol.Enable == p.info.Protocol.Enable && !old.needsReplacement(p) {
			merged[i] = old
			retained[key] = true
			continue
		}
		if p.info.Protocol.Enable {
			toStart = append(toStart, p)
		}
	}
	for _, p := range previous {
		if p.info.Protocol.Enable && !retained[protocolIdentity(p.info.Protocol)] {
			toStop = append(toStop, p)
		}
	}
	return merged, toStart, toStop
}

func runProtocol(action string, p protocolInstance, fn func() error) error {
	if err := fn(); err != nil {
		key := protocolIdentity(p.info.Protocol)
		return fmt.Errorf("%s %s(%s): %w", action, key.typ, key.id, err)
	}
	return nil
}
