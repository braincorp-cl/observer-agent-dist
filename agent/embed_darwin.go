//go:build darwin
// +build darwin

package agent

import _ "embed"

//go:embed scripts/macos_fix_mesh_install.sh
var ventura_mesh_fix string

func (a *Agent) FixVenturaMesh() {
	a.RunScript(ventura_mesh_fix, "foo", []string{}, 45, false, []string{}, false, "")
}
