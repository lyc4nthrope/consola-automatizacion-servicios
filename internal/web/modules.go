package web

// Module is one section of the console. The list below is the single source for the
// sidebar, the panel and the module pages.
type Module struct {
	Slug    string // URL path without the slash
	Title   string
	Icon    string // Bootstrap Icons class
	Summary string
	Actions []string
}

// Path returns the URL of the module page.
func (m Module) Path() string { return "/" + m.Slug }

var modules = []Module{
	{
		Slug:    "preparacion",
		Title:   "Preparación del sistema",
		Icon:    "bi-gear",
		Summary: "Paquetes, llaves de root, mensaje de entrada y nombre de host de la VM.",
		Actions: []string{"Instalar y eliminar aplicaciones", "Llaves SSH de root", "Mensaje de entrada (/etc/issue)", "Nombre de host y /etc/hosts"},
	},
	{
		Slug:    "usuarios",
		Title:   "Gestión de usuarios",
		Icon:    "bi-person-check",
		Summary: "Cuentas del sistema con privilegios de sudo y llaves de acceso propias.",
		Actions: []string{"Crear usuario", "Privilegios de sudo", "Llave SSH por usuario", "Eliminar usuario"},
	},
	{
		Slug:    "servicios",
		Title:   "Servicios systemd",
		Icon:    "bi-hdd-stack",
		Summary: "Desplegar aplicaciones como servicios y controlar su ciclo de vida.",
		Actions: []string{"Desplegar aplicación", "Iniciar, detener y reiniciar", "Consultar estado", "Habilitar al arranque"},
	},
	{
		Slug:    "balanceador",
		Title:   "Balanceador HAProxy",
		Icon:    "bi-diagram-3",
		Summary: "Servidores que reciben el tráfico del balanceador de carga.",
		Actions: []string{"Agregar servidor al back-end", "Quitar servidor del back-end"},
	},
}
