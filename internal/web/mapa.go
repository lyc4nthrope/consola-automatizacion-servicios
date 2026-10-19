package web

import "github.com/lyc4nthrope/consola-automatizacion-servicios/internal/app"

// Part A, F6b: the infrastructure map on the panel. It shows the machine running the
// console, VirtualBox and every VM of config.json with the channel the console uses to
// reach it; choosing a VM makes it the current target.

// mapaVM is one VM on the map.
type mapaVM struct {
	app.Target
	Channel string // as the channel names itself: "Guest Additions", "SSH", "Simulado", "Sin conexión"
	Kind    string // line style: "ga", "ssh", "sim", "none" or "otro"
	Current bool
}

// channelKind maps a channel name to its line on the map.
func channelKind(channel string) string {
	switch channel {
	case "Guest Additions":
		return "ga"
	case "SSH":
		return "ssh"
	case "Simulado":
		return "sim"
	case "Sin conexión":
		return "none"
	}
	return "otro"
}

func (s *server) mapa() []mapaVM {
	cur := s.state.Current().Name
	var out []mapaVM
	for _, t := range s.state.Targets() {
		ch := s.state.Channel(t.Name)
		out = append(out, mapaVM{Target: t, Channel: ch, Kind: channelKind(ch), Current: t.Name == cur})
	}
	return out
}
