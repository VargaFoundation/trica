package topomap

import "github.com/VargaFoundation/trica/internal/live"

// rates returns the traffic of one link as seen from its "a" end, in bits per
// second: a to b, b to a. The a end's counters are used when it has any,
// else the b end's, swapped; counting both would measure the same traffic
// twice. measured is false when neither end has a rate.
func rates(l *link, lv map[string]live.Link) (ab, ba *float64, measured bool) {
	if lv == nil {
		return nil, nil, false
	}
	if x, ok := lv[l.a]; ok && (x.TX != nil || x.RX != nil) {
		return x.TX, x.RX, true
	}
	if y, ok := lv[l.b]; ok && (y.TX != nil || y.RX != nil) {
		return y.RX, y.TX, true
	}
	return nil, nil, false
}

// operOf is the state of a link: down when an end is down, up when an end is
// up and none down, unknown otherwise. The live gauge wins over the
// inventory's last observed oper_status.
func (ix *Index) operOf(l *link, lv map[string]live.Link) string {
	up, down := false, false
	for _, id := range [2]string{l.a, l.b} {
		s := ""
		if x, ok := lv[id]; ok {
			s = x.Oper
		}
		if s == "" {
			s = attrStr(ix.Snap.Nodes[id], "oper_status")
		}
		switch s {
		case "up":
			up = true
		case "down":
			down = true
		}
	}
	switch {
	case down:
		return "down"
	case up:
		return "up"
	}
	return "unknown"
}

func addRate(sum **float64, v *float64) {
	if v == nil {
		return
	}
	if *sum == nil {
		z := 0.0
		*sum = &z
	}
	**sum += *v
}
