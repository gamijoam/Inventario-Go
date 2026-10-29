package tui

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"charm.land/bubbles/v2/table"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/gamijoam/inventario-go/internal/application/auth"
	apppricing "github.com/gamijoam/inventario-go/internal/application/pricing"
	"github.com/gamijoam/inventario-go/internal/domain/identity"
	"github.com/gamijoam/inventario-go/internal/domain/org"
)

// ---------- Listas de precios ----------

func newPriceLists(a *app) screen {
	r := &recordsScreen[apppricing.ListView]{app: a, title: "Listas de precios",
		info: "Cada lista tiene una moneda y un margen objetivo sobre el costo de reposición (o, sin él, el último costo de " +
			"compra). Al recibir mercancía, el sistema propone precios con ese margen; nadie los cambia sin aprobarlos.",
		empty: "No hay listas de precios.",
		list: newList([]table.Column{{Title: "Código", Width: 12}, {Title: "Nombre", Width: 30}, {Title: "Moneda", Width: 6},
			{Title: "Margen objetivo", Width: 15}, {Title: "Estado", Width: 8}}, 1),
		fetch: func(bool) ([]apppricing.ListView, error) { return a.opts.Backend.PriceLists(a.ctx) },
		row: func(v apppricing.ListView) table.Row {
			margin, state := "sin margen", "activa"
			if v.Margin != nil {
				margin = v.Margin.String() + " %"
			}
			if v.Status != org.StatusActive {
				state = "inactiva"
			}
			return table.Row{v.Code.String(), v.Name, v.Currency.Code(), margin, state}
		}}
	manage := func(*apppricing.ListView) bool { return a.can(identity.PermPriceListManage) }
	r.actions = []action[apppricing.ListView]{
		{key: "f2", label: "F2 Nueva", ok: manage, run: func(*apppricing.ListView) tea.Cmd { return push(newPriceListForm(a)) }},
		{key: "f3", label: "F3 Margen", needsItem: true, ok: manage, run: func(v *apppricing.ListView) tea.Cmd { return push(newMarginForm(a, *v, false)) }},
		{key: "f4", label: "F4 Margen de producto", needsItem: true, ok: manage,
			run: func(v *apppricing.ListView) tea.Cmd { return push(newMarginForm(a, *v, true)) }},
	}
	return r
}

func newPriceListForm(a *app) screen {
	f := &formScreen{app: a, title: "Nueva lista de precios",
		info: "Código corto (GENERAL, MAYOR). La moneda no se cambia después. El margen es un porcentaje sobre el costo; " +
			"vacío, la lista no sugiere precios hasta que lo fije.",
		fields: []*field{textF("code", "Código", "", 16), textF("name", "Nombre", "", 120),
			choiceF("currency", "Moneda", currencyCodes, "").labeled(currencyLabels...),
			textF("margin", "Margen objetivo %", "", 16).placeholder("opcional, p. ej. 25")}}
	f.save = func(v map[string]string) (any, error) {
		return a.opts.Backend.CreatePriceList(a.ctx, apppricing.ListInput{Code: v["code"], Name: v["name"], Currency: v["currency"], Margin: v["margin"]})
	}
	f.done = func(v any) tea.Cmd { return back("lista creada: " + v.(apppricing.ListView).Code.String()) }
	return f
}

func newMarginForm(a *app, l apppricing.ListView, product bool) screen {
	title, info := "Margen de "+l.Code.String(), "El margen objetivo de la lista. Vacío deja la lista sin margen. Cada cambio queda versionado."
	fields := []*field{textF("margin", "Margen objetivo %", "", 16)}
	if l.Margin != nil {
		fields[0].input.SetValue(l.Margin.String())
	}
	if product {
		title, info = "Margen de un producto en "+l.Code.String(), "Excepción para un producto. Vacío vuelve a usar el margen de la lista."
		fields = append([]*field{productF("product", "Producto")}, textF("margin", "Margen objetivo %", "", 16))
	}
	f := &formScreen{app: a, title: title, info: info, fields: fields}
	f.save = func(v map[string]string) (any, error) {
		return a.opts.Backend.SetMargin(a.ctx, apppricing.MarginInput{List: l.Code.String(), Product: v["product"], Margin: v["margin"]})
	}
	f.done = func(any) tea.Cmd { return back("margen actualizado en " + l.Code.String()) }
	return f
}

// newPropose registra una propuesta manual; list y product la preparan.
func newPropose(a *app, list, product string) screen {
	prod := productF("product", "Producto")
	prod.input.SetValue(product)
	f := &formScreen{app: a, title: "Proponer precio",
		info: "La propuesta no cambia el precio vigente: queda pendiente hasta que alguien con permiso la apruebe " +
			"(puede ser usted mismo si lo tiene).",
		fields: []*field{choiceF("list", "Lista", nil, ""), prod, textF("price", "Precio", "", 32), textF("note", "Nota", "", 500)}}
	f.prepare = func() (any, error) { return a.opts.Backend.PriceLists(a.ctx) }
	f.ready = func(f *formScreen, v any) {
		fl := f.field("list")
		for _, l := range v.([]apppricing.ListView) {
			if l.Status == org.StatusActive {
				fl.options = append(fl.options, l.Code.String())
				fl.labels = append(fl.labels, l.Code.String()+" · "+l.Name+" ("+l.Currency.Code()+")")
				if l.Code.String() == list {
					fl.choice = len(fl.options) - 1
				}
			}
		}
		if len(fl.options) == 0 {
			fl.options, fl.labels = []string{""}, []string{"no hay listas: créelas en Precios → Listas de precios"}
		}
	}
	f.save = func(v map[string]string) (any, error) {
		return a.opts.Backend.Propose(a.ctx, apppricing.ProposeInput{List: v["list"], Product: v["product"], Price: v["price"], Note: v["note"]})
	}
	f.done = func(v any) tea.Cmd {
		p := v.(apppricing.ProposalView)
		return back(fmt.Sprintf("propuesta %s: %s %s en %s, pendiente de aprobación", p.Number, p.Product.SKU, p.Price, p.List.Code))
	}
	return f
}

// ---------- Usuarios ----------

func userState(u auth.UserView) string {
	switch {
	case u.Status != identity.StatusActive:
		return "inactivo"
	case u.Locked:
		return "bloqueado"
	}
	return "activo"
}

func assignmentsText(u auth.UserView) string {
	parts := make([]string, len(u.Assignments))
	for i, x := range u.Assignments {
		parts[i] = x.RoleCode.String() + " en " + x.Where.String()
	}
	if len(parts) == 0 {
		return "sin roles"
	}
	return strings.Join(parts, "; ")
}

func newUsers(a *app) screen {
	r := &recordsScreen[auth.UserView]{app: a, title: "Usuarios", wide: true,
		info:  "Un usuario no puede hacer nada hasta que se le asigne un rol en un ámbito.",
		empty: "No hay usuarios visibles.",
		list: newList([]table.Column{{Title: "Usuario", Width: 16}, {Title: "Nombre", Width: 24}, {Title: "Estado", Width: 9},
			{Title: "Roles", Width: 60}}, 3),
		fetch: func(bool) ([]auth.UserView, error) { return a.opts.Backend.Users(a.ctx) },
		row: func(u auth.UserView) table.Row {
			return table.Row{u.Username.String(), u.DisplayName.String(), userState(u), assignmentsText(u)}
		}}
	r.actions = []action[auth.UserView]{
		{key: "f2", label: "F2 Nuevo", ok: func(*auth.UserView) bool { return a.can(identity.PermUserManage) },
			run: func(*auth.UserView) tea.Cmd { return push(newUserForm(a)) }},
		{key: "enter", label: "Enter Abrir", needsItem: true, run: func(u *auth.UserView) tea.Cmd { return push(newUser(a, u.Username.String())) }},
	}
	return r
}

func newUserForm(a *app) screen {
	f := &formScreen{app: a, title: "Nuevo usuario",
		info: "Nombre de usuario en minúsculas, sin espacios. Después ábralo y asígnele un rol (F2) para que pueda trabajar.",
		fields: []*field{textF("user", "Usuario", "", 64), textF("name", "Nombre visible", "", 120),
			passwordF("password", "Contraseña"), passwordF("again", "Repita la contraseña")}}
	f.save = func(v map[string]string) (any, error) {
		if v["password"] != v["again"] {
			return nil, errors.New("las contraseñas no coinciden")
		}
		return a.opts.Backend.CreateUser(a.ctx, auth.NewUserInput{Username: v["user"], DisplayName: v["name"], Password: v["password"]})
	}
	f.done = func(v any) tea.Cmd {
		u := v.(identity.User)
		return replace(newUser(a, u.Username.String()), "usuario "+u.Username.String()+" creado; asígnele un rol con F2")
	}
	return f
}

// userScreen muestra un usuario con sus asignaciones.
type userScreen struct {
	app      *app
	id       int
	username string
	v        *auth.UserView
	list     listView
}

func newUser(a *app, username string) screen {
	return &userScreen{app: a, username: username, list: newList([]table.Column{{Title: "Rol", Width: 20}, {Title: "Ámbito", Width: 50},
		{Title: "Desde", Width: 16}}, 1)}
}

func (u *userScreen) Title() string   { return "Usuario " + u.username }
func (u *userScreen) Capturing() bool { return u.list.capturing() }
func (u *userScreen) Info() string {
	return "Los permisos de un usuario son la suma de sus roles, cada uno en su ámbito: toda la organización, una " +
		"empresa, una sucursal o un almacén."
}

func (u *userScreen) manage() bool { return u.v != nil && u.app.can(identity.PermUserManage) }

func (u *userScreen) Help() string {
	if !u.manage() {
		return "↑↓ Elegir  R Recargar  Esc Volver"
	}
	parts := []string{"↑↓ Elegir", "F2 Asignar rol"}
	if u.list.selected() >= 0 {
		parts = append(parts, "F8 Quitar")
	}
	parts = append(parts, "F3 Activar/Inactivar", "F5 Restablecer contraseña")
	if u.v.Locked {
		parts = append(parts, "F6 Desbloquear")
	}
	return strings.Join(append(parts, "R Recargar", "Esc Volver"), "  ")
}

func (u *userScreen) Init(id int) tea.Cmd {
	u.id = id
	return u.Refresh()
}

func (u *userScreen) Refresh() tea.Cmd {
	a, name := u.app, u.username
	return load(u.id, func() (any, error) {
		list, err := a.opts.Backend.Users(a.ctx)
		if err != nil {
			return nil, err
		}
		for _, x := range list {
			if x.Username.String() == name {
				return x, nil
			}
		}
		return nil, fmt.Errorf("no se encontró el usuario %s", name)
	})
}

func (u *userScreen) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		u.list.resize(msg.Width, msg.Height-2)
	case loaded:
		if msg.err != nil {
			return setStatus("no se pudo cargar el usuario", msg.err)
		}
		v := msg.v.(auth.UserView)
		u.v = &v
		rows := make([]table.Row, len(v.Assignments))
		for i, x := range v.Assignments {
			rows[i] = table.Row{x.RoleCode.String(), x.Where.String(), when(x.CreatedAt)}
		}
		u.list.setRows(rows)
	case tea.KeyPressMsg:
		if cmd, done := u.list.update(msg); done {
			return cmd
		}
		if msg.String() == "r" {
			return u.Refresh()
		}
		if !u.manage() {
			return nil
		}
		a, v := u.app, *u.v
		name := v.Username.String()
		switch msg.String() {
		case "f2":
			return push(newAssign(a, name))
		case "f8":
			if i := u.list.selected(); i >= 0 {
				x := v.Assignments[i]
				return push(&confirmScreen{app: a, title: "Quitar rol",
					message: fmt.Sprintf("Quitar el rol %s en %s a %s. Pierde esos permisos en su próxima operación.", x.RoleCode, x.Where, name),
					ok:      "rol quitado",
					run: func() (any, error) {
						return nil, a.opts.Backend.Unassign(a.ctx, name, x.RoleCode.String(), x.Where)
					}})
			}
		case "f3":
			active := v.Status != identity.StatusActive
			verb, note := "Inactivar", "No podrá iniciar sesión; sus sesiones abiertas se cierran. Su historial se conserva."
			if active {
				verb, note = "Activar", "Podrá volver a iniciar sesión con su contraseña."
			}
			return push(&confirmScreen{app: a, title: verb + " " + name, message: verb + " a " + name + ". " + note,
				ok:  "usuario " + name + " " + strings.ToLower(verb[:len(verb)-1]) + "do",
				run: func() (any, error) { return a.opts.Backend.SetUserActive(a.ctx, name, active) }})
		case "f5":
			return push(newResetPassword(a, name))
		case "f6":
			if v.Locked {
				return push(&confirmScreen{app: a, title: "Desbloquear " + name,
					message: name + " quedó bloqueado por intentos fallidos. Desbloquearlo le permite intentar de nuevo ya.",
					ok:      "usuario " + name + " desbloqueado", run: func() (any, error) { return nil, a.opts.Backend.Unlock(a.ctx, name) }})
			}
		}
	default:
		cmd, _ := u.list.update(msg)
		return cmd
	}
	return nil
}

func (u *userScreen) View(int, int) string {
	if u.v == nil {
		return "Cargando…"
	}
	head := fmt.Sprintf("%s · %s", u.v.DisplayName, userState(*u.v))
	return head + "\n" + u.list.view("Sin roles: no puede hacer nada. Pulse F2 para asignarle uno.")
}

func newResetPassword(a *app, name string) screen {
	f := &formScreen{app: a, title: "Restablecer contraseña de " + name,
		info:   "Fija una contraseña nueva y cierra todas las sesiones del usuario. Comuníquesela por un medio seguro.",
		fields: []*field{passwordF("password", "Contraseña nueva"), passwordF("again", "Repita la contraseña")}}
	f.save = func(v map[string]string) (any, error) {
		if v["password"] != v["again"] {
			return nil, errors.New("las contraseñas no coinciden")
		}
		return nil, a.opts.Backend.ResetPassword(a.ctx, name, v["password"])
	}
	f.done = func(any) tea.Cmd { return back("contraseña de " + name + " restablecida") }
	return f
}

// newChangePassword cambia la contraseña propia.
func newChangePassword(a *app) screen {
	f := &formScreen{app: a, title: "Cambiar mi contraseña",
		info: "Pide la actual. Al cambiarla se cierran sus otras sesiones; esta sigue abierta.",
		fields: []*field{passwordF("current", "Contraseña actual"), passwordF("password", "Contraseña nueva"),
			passwordF("again", "Repita la nueva")}}
	f.save = func(v map[string]string) (any, error) {
		if v["password"] != v["again"] {
			return nil, errors.New("las contraseñas nuevas no coinciden")
		}
		return nil, a.opts.Backend.ChangePassword(a.ctx, v["current"], v["password"])
	}
	f.done = func(any) tea.Cmd { return back("contraseña cambiada") }
	return f
}

// ---------- Ámbito en cascada ----------

// structure es la organización visible: empresas, sus sucursales y los
// almacenes de cada sucursal.
type structure struct {
	companies  []org.Company
	branches   map[string][]org.Branch    // por empresa
	warehouses map[string][]org.Warehouse // por "empresa/sucursal"
}

func loadStructure(a *app) (structure, error) {
	s := structure{branches: map[string][]org.Branch{}, warehouses: map[string][]org.Warehouse{}}
	var err error
	if s.companies, err = a.opts.Backend.Companies(a.ctx); err != nil {
		return s, err
	}
	for _, c := range s.companies {
		cc := c.Code.String()
		if s.branches[cc], err = a.opts.Backend.Branches(a.ctx, cc); err != nil {
			return s, err
		}
		for _, b := range s.branches[cc] {
			if s.warehouses[cc+"/"+b.Code.String()], err = a.opts.Backend.WarehousesOf(a.ctx, cc, b.Code.String()); err != nil {
				return s, err
			}
		}
	}
	return s, nil
}

var levels = []string{"org", "company", "branch", "warehouse"}

func newAssign(a *app, username string) screen {
	var st structure
	f := &formScreen{app: a, title: "Asignar rol a " + username,
		info: "Elija el rol y dónde rige: en toda la organización, en una empresa (y todas sus sucursales), en una " +
			"sucursal (y sus almacenes) o en un solo almacén.",
		fields: []*field{choiceF("role", "Rol", nil, ""),
			choiceF("level", "Ámbito", levels, "branch").labeled("Toda la organización", "Una empresa", "Una sucursal", "Un almacén"),
			choiceF("company", "Empresa", nil, ""), choiceF("branch", "Sucursal", nil, ""), choiceF("warehouse", "Almacén", nil, "")}}
	type prep struct {
		roles []identity.Role
		st    structure
	}
	f.prepare = func() (any, error) {
		roles, err := a.opts.Backend.Roles(a.ctx)
		if err != nil {
			return nil, err
		}
		s, err := loadStructure(a)
		return prep{roles, s}, err
	}
	// cascade recalcula las opciones de cada nivel según los de arriba.
	cascade := func(f *formScreen, _ string) {
		level := f.field("level").choice
		set := func(key string, need int, codes, labels []string) {
			fl := f.field(key)
			if level < need || len(codes) == 0 {
				fl.options, fl.labels, fl.choice = []string{""}, []string{"—"}, 0
				return
			}
			if fl.choice >= len(codes) || (len(fl.options) > 0 && fl.options[0] == "") {
				fl.choice = 0
			}
			fl.options, fl.labels = codes, labels
		}
		var cc, cl []string
		for _, c := range st.companies {
			cc, cl = append(cc, c.Code.String()), append(cl, c.Code.String()+" · "+c.LegalName.String())
		}
		set("company", 1, cc, cl)
		company := f.field("company").value()
		var bc, bl []string
		for _, b := range st.branches[company] {
			bc, bl = append(bc, b.Code.String()), append(bl, b.Code.String()+" · "+b.Name.String())
		}
		set("branch", 2, bc, bl)
		var wc, wl []string
		for _, w := range st.warehouses[company+"/"+f.field("branch").value()] {
			wc, wl = append(wc, w.Code.String()), append(wl, w.Code.String()+" · "+w.Name.String())
		}
		set("warehouse", 3, wc, wl)
	}
	f.changed = cascade
	f.ready = func(f *formScreen, v any) {
		p := v.(prep)
		st = p.st
		fl := f.field("role")
		for _, r := range p.roles {
			fl.options = append(fl.options, r.Code.String())
			fl.labels = append(fl.labels, r.Code.String()+" · "+r.Name.String())
		}
		cascade(f, "")
	}
	f.save = func(v map[string]string) (any, error) {
		where := auth.ScopeRef{Company: v["company"], Branch: v["branch"], Warehouse: v["warehouse"]}
		return a.opts.Backend.Assign(a.ctx, username, v["role"], where)
	}
	f.done = func(v any) tea.Cmd {
		x := v.(auth.AssignmentView)
		return back("rol " + x.RoleCode.String() + " asignado en " + x.Where.String())
	}
	return f
}

// ---------- Roles ----------

// templateCodes son los roles sembrados al configurar: se ven pero no se
// editan desde la TUI (se copian como rol nuevo).
func isTemplate(code string) bool {
	return slices.ContainsFunc(identity.Templates(), func(t identity.RoleTemplate) bool { return t.Code == code })
}

func newRoles(a *app) screen {
	r := &recordsScreen[identity.Role]{app: a, title: "Roles",
		info: "Un rol es un conjunto de permisos. Los roles de fábrica (ADMIN_ORG, CAJERO…) se copian para ajustarlos; " +
			"los permisos extraordinarios nunca vienen en ellos.",
		empty: "No hay roles.",
		list: newList([]table.Column{{Title: "Código", Width: 20}, {Title: "Nombre", Width: 30}, {Title: "Permisos", Width: 8},
			{Title: "Tipo", Width: 10}}, 1),
		fetch: func(bool) ([]identity.Role, error) { return a.opts.Backend.Roles(a.ctx) },
		row: func(x identity.Role) table.Row {
			kind := "propio"
			if isTemplate(x.Code.String()) {
				kind = "de fábrica"
			}
			return table.Row{x.Code.String(), x.Name.String(), itoa(len(x.Permissions)), kind}
		}}
	manage := func(*identity.Role) bool { return a.can(identity.PermRoleManage) }
	r.actions = []action[identity.Role]{
		{key: "f2", label: "F2 Nuevo", ok: manage, run: func(*identity.Role) tea.Cmd { return push(newRoleEditor(a, nil, false)) }},
		{key: "f7", label: "F7 Copiar como nuevo", needsItem: true, ok: manage,
			run: func(x *identity.Role) tea.Cmd { return push(newRoleEditor(a, x, true)) }},
		{key: "enter", label: "Enter Abrir", needsItem: true, run: func(x *identity.Role) tea.Cmd { return push(newRoleEditor(a, x, false)) }},
	}
	return r
}

// roleEditor muestra los permisos como casillas. Con un rol propio y
// permiso, Espacio marca o desmarca y F10 guarda; uno nuevo pide además
// código y nombre.
type roleEditor struct {
	app      *app
	id       int
	role     *identity.Role // nil: nuevo
	readOnly bool
	code     textinput.Model
	name     textinput.Model
	perms    []identity.PermissionInfo
	checked  map[identity.Permission]bool
	cursor   int // 0 y 1 son código y nombre si es nuevo; luego las casillas
	top      int
	height   int
	busy     bool
	errMsg   string
}

func newRoleEditor(a *app, r *identity.Role, copy bool) screen {
	e := &roleEditor{app: a, perms: identity.Catalog(), checked: map[identity.Permission]bool{}, height: 10,
		code: newInput("Código: ", 32), name: newInput("Nombre: ", 120)}
	if r != nil {
		for _, p := range r.Permissions {
			e.checked[p] = true
		}
	}
	switch {
	case copy:
		e.name.SetValue(r.Name.String() + " (copia)")
	case r != nil:
		e.role = r
		e.readOnly = isTemplate(r.Code.String()) || !a.can(identity.PermRoleManage)
	}
	return e
}

func (e *roleEditor) isNew() bool { return e.role == nil }

func (e *roleEditor) Title() string {
	switch {
	case e.isNew():
		return "Nuevo rol"
	case e.readOnly:
		return "Rol " + e.role.Code.String() + " (solo lectura)"
	}
	return "Rol " + e.role.Code.String()
}

func (e *roleEditor) Capturing() bool { return !e.readOnly }
func (e *roleEditor) Wide() bool      { return true }
func (e *roleEditor) Help() string {
	if e.readOnly {
		return "↑↓ Elegir  Esc Volver"
	}
	return "↑↓ Elegir  Espacio Marcar  F10 Guardar  Esc Cancelar"
}

func (e *roleEditor) offset() int {
	if e.isNew() {
		return 2
	}
	return 0
}

func (e *roleEditor) Init(id int) tea.Cmd {
	e.id = id
	if e.isNew() {
		return e.code.Focus()
	}
	return nil
}

func (e *roleEditor) focus() tea.Cmd {
	e.code.Blur()
	e.name.Blur()
	switch {
	case e.isNew() && e.cursor == 0:
		return e.code.Focus()
	case e.isNew() && e.cursor == 1:
		return e.name.Focus()
	}
	return nil
}

func (e *roleEditor) save() tea.Cmd {
	e.busy, e.errMsg = true, ""
	a := e.app
	if e.isNew() {
		var perms []string
		for _, p := range e.perms {
			if e.checked[p.Permission] {
				perms = append(perms, string(p.Permission))
			}
		}
		code, name := e.code.Value(), e.name.Value()
		return load(e.id, func() (any, error) { return a.opts.Backend.CreateRole(a.ctx, code, name, perms) })
	}
	var add, remove []string
	for _, p := range e.perms {
		had := e.role.Has(p.Permission)
		switch {
		case e.checked[p.Permission] && !had:
			add = append(add, string(p.Permission))
		case !e.checked[p.Permission] && had:
			remove = append(remove, string(p.Permission))
		}
	}
	if len(add)+len(remove) == 0 {
		e.busy = false
		return back("sin cambios")
	}
	code := e.role.Code.String()
	return load(e.id, func() (any, error) { return a.opts.Backend.EditRole(a.ctx, code, add, remove) })
}

func (e *roleEditor) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		e.height = max(msg.Height-4, 3)
	case loaded:
		e.busy = false
		if msg.err != nil {
			e.errMsg = describe(msg.err)
			return nil
		}
		r := msg.v.(identity.Role)
		if e.isNew() {
			return back("rol " + r.Code.String() + " creado")
		}
		return back(fmt.Sprintf("rol %s actualizado: %d permisos", r.Code, len(r.Permissions)))
	case tea.KeyPressMsg:
		if e.busy {
			return nil
		}
		n := e.offset() + len(e.perms)
		switch msg.String() {
		case "esc":
			if e.readOnly {
				return back("")
			}
			return back("cancelado; no se guardó nada")
		case "up", "shift+tab":
			e.cursor = (e.cursor - 1 + n) % n
			return e.focus()
		case "down", "tab", "enter":
			e.cursor = (e.cursor + 1) % n
			return e.focus()
		case "pgdown":
			e.cursor = min(e.cursor+e.height, n-1)
			return e.focus()
		case "pgup":
			e.cursor = max(e.cursor-e.height, 0)
			return e.focus()
		}
		if e.readOnly {
			return nil
		}
		switch msg.String() {
		case "f10", "ctrl+s":
			return e.save()
		case "space", " ":
			if i := e.cursor - e.offset(); i >= 0 {
				p := e.perms[i].Permission
				e.checked[p] = !e.checked[p]
				return nil
			}
		}
		var cmd tea.Cmd
		switch {
		case e.isNew() && e.cursor == 0:
			e.code, cmd = e.code.Update(msg)
		case e.isNew() && e.cursor == 1:
			e.name, cmd = e.name.Update(msg)
		}
		return cmd
	}
	return nil
}

func (e *roleEditor) View(int, int) string {
	var b strings.Builder
	if e.isNew() {
		b.WriteString(e.code.View() + "\n" + e.name.View() + "\n\n")
	} else {
		b.WriteString(e.role.Name.String() + "\n\n")
	}
	rows := e.height - strings.Count(b.String(), "\n") - 2
	i := e.cursor - e.offset()
	// Mantiene la casilla elegida a la vista.
	switch {
	case i < 0:
		e.top = 0
	case i < e.top:
		e.top = i
	case i >= e.top+rows:
		e.top = i - rows + 1
	}
	prev := ""
	for j := e.top; j < len(e.perms) && j < e.top+rows; j++ {
		p := e.perms[j]
		mark := "[ ]"
		if e.checked[p.Permission] {
			mark = "[x]"
		}
		module := strings.SplitN(string(p.Permission), ".", 2)[0]
		if p.Extraordinary {
			module = "extraordinary"
		}
		group := ""
		if module != prev {
			group = cmpOr(moduleText[module], module)
		}
		prev = module
		extra := ""
		if p.Extraordinary {
			extra = warnStyle.Render(" (extraordinario)")
		}
		line := fmt.Sprintf("%-11s %s %s%s", group, mark, p.Description, extra)
		if j == i {
			line = pickedStyle.Render(line)
		}
		b.WriteString(line + "\n")
	}
	count := 0
	for _, v := range e.checked {
		if v {
			count++
		}
	}
	b.WriteString(hintStyle.Render(fmt.Sprintf("%d de %d permisos marcados", count, len(e.perms))))
	switch {
	case e.busy:
		b.WriteString("\nGuardando…")
	case e.errMsg != "":
		b.WriteString("\n" + failStyle.Render(e.errMsg))
	}
	return b.String()
}

// moduleText nombra cada grupo de permisos.
var moduleText = map[string]string{"org": "Estructura", "identity": "Usuarios", "catalog": "Catálogo", "inventory": "Inventario",
	"purchase": "Compras", "currency": "Tasas", "pricing": "Precios", "system": "Sistema", "extraordinary": "Extraord."}

// ---------- Estructura ----------

// node es una fila del árbol de la organización.
type node struct {
	level              int // 0 empresa, 1 sucursal, 2 almacén
	company, branch    string
	code, name, detail string
}

func newStructure(a *app) screen {
	r := &recordsScreen[node]{app: a, title: "Estructura",
		info: "Empresas legales, sus sucursales y los almacenes de cada sucursal. La moneda de valoración de una empresa " +
			"no se cambia después de crearla.",
		empty: "Sin estructura visible.",
		list:  newList([]table.Column{{Title: "Código", Width: 26}, {Title: "Nombre", Width: 40}, {Title: "Detalle", Width: 30}}, 1),
		fetch: func(bool) ([]node, error) {
			st, err := loadStructure(a)
			if err != nil {
				return nil, err
			}
			var out []node
			for _, c := range st.companies {
				cc := c.Code.String()
				detail := "valoración " + c.Currency.Code()
				if c.RIF.String() != "" {
					detail = "RIF " + c.RIF.String() + " · " + detail
				}
				out = append(out, node{0, cc, "", cc, c.LegalName.String(), detail})
				for _, b := range st.branches[cc] {
					bc := b.Code.String()
					out = append(out, node{1, cc, bc, bc, b.Name.String(), "sucursal"})
					for _, w := range st.warehouses[cc+"/"+bc] {
						out = append(out, node{2, cc, bc, w.Code.String(), w.Name.String(), "almacén"})
					}
				}
			}
			return out, nil
		},
		row: func(n node) table.Row {
			return table.Row{strings.Repeat("  ", n.level) + n.code, n.name, n.detail}
		}}
	r.actions = []action[node]{
		{key: "f2", label: "F2 Empresa", ok: func(*node) bool { return a.can(identity.PermCompanyCreate) },
			run: func(*node) tea.Cmd { return push(newCompanyForm(a)) }},
		{key: "f3", label: "F3 Sucursal", needsItem: true, ok: func(*node) bool { return a.can(identity.PermBranchCreate) },
			run: func(n *node) tea.Cmd { return push(newBranchForm(a, n.company)) }},
		{key: "f4", label: "F4 Almacén", needsItem: true,
			ok:  func(n *node) bool { return n != nil && n.level > 0 && a.can(identity.PermWarehouseCreate) },
			run: func(n *node) tea.Cmd { return push(newWarehouseForm(a, n.company, n.branch)) }},
	}
	return r
}

func newCompanyForm(a *app) screen {
	f := &formScreen{app: a, title: "Nueva empresa",
		info: "Una empresa legal con su RIF y su moneda de valoración (no se cambia después). Luego créele una sucursal.",
		fields: []*field{textF("code", "Código", "", 16), textF("legal", "Razón social", "", 120),
			textF("rif", "RIF", "", 16).placeholder("opcional: J-12345678-9"), choiceF("currency", "Moneda de valoración", []string{"USD", "VES"}, "USD")}}
	f.save = func(v map[string]string) (any, error) {
		return a.opts.Backend.CreateCompany(a.ctx, v["code"], v["legal"], v["rif"], v["currency"])
	}
	f.done = func(v any) tea.Cmd { return back("empresa creada: " + v.(org.Company).Code.String()) }
	return f
}

func newBranchForm(a *app, company string) screen {
	f := &formScreen{app: a, title: "Nueva sucursal en " + company,
		info:   "Luego créele al menos un almacén (F4 sobre la sucursal).",
		fields: []*field{textF("code", "Código", "", 16), textF("name", "Nombre", "", 120)}}
	f.save = func(v map[string]string) (any, error) {
		return a.opts.Backend.CreateBranch(a.ctx, company, v["code"], v["name"])
	}
	f.done = func(v any) tea.Cmd { return back("sucursal creada: " + company + "/" + v.(org.Branch).Code.String()) }
	return f
}

func newWarehouseForm(a *app, company, branch string) screen {
	f := &formScreen{app: a, title: "Nuevo almacén en " + company + "/" + branch,
		info:   "Un depósito o área de la sucursal con su propia existencia.",
		fields: []*field{textF("code", "Código", "", 16), textF("name", "Nombre", "", 120)}}
	f.save = func(v map[string]string) (any, error) {
		return a.opts.Backend.CreateWarehouse(a.ctx, company, branch, v["code"], v["name"])
	}
	f.done = func(v any) tea.Cmd {
		return back("almacén creado: " + company + "/" + branch + "/" + v.(org.Warehouse).Code.String())
	}
	return f
}
