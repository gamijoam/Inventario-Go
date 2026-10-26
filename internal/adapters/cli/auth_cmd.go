package cli

import (
	"context"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/gamijoam/inventario-go/internal/application/auth"
	"github.com/gamijoam/inventario-go/internal/domain/identity"
)

type assignmentJSON struct {
	Role      string `json:"role"`
	Scope     string `json:"scope"` // organization, company, branch o warehouse
	Company   string `json:"company_code,omitempty"`
	Branch    string `json:"branch_code,omitempty"`
	Warehouse string `json:"warehouse_code,omitempty"`
}

type userJSON struct {
	ID          string           `json:"id"`
	Username    string           `json:"username"`
	DisplayName string           `json:"display_name"`
	Status      string           `json:"status"`
	Locked      bool             `json:"locked"`
	CreatedAt   string           `json:"created_at"`
	Assignments []assignmentJSON `json:"assignments"`
}

func toAssignmentJSON(a auth.AssignmentView) assignmentJSON {
	return assignmentJSON{Role: a.RoleCode.String(), Scope: a.Scope.Level().String(), Company: a.Where.Company, Branch: a.Where.Branch, Warehouse: a.Where.Warehouse}
}

func toUserJSON(v auth.UserView) userJSON {
	out := userJSON{ID: v.ID.String(), Username: v.Username.String(), DisplayName: v.DisplayName.String(),
		Status: string(v.Status), Locked: v.Locked, CreatedAt: rfc3339(v.CreatedAt), Assignments: []assignmentJSON{}}
	for _, a := range v.Assignments {
		out.Assignments = append(out.Assignments, toAssignmentJSON(a))
	}
	return out
}

func describeAssignment(a auth.AssignmentView) string {
	return a.RoleCode.String() + " en " + a.Where.String()
}

func newAuthCmd(env Env, flags *globalFlags) *cobra.Command {
	cmd := &cobra.Command{Use: "auth", Short: "Inicio y cierre de sesión", Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() }}

	var user string
	var passwordStdin bool
	login := &cobra.Command{
		Use:   "login",
		Short: "Inicia sesión en este equipo",
		Long: "Verifica usuario y contraseña y guarda un token de sesión en este equipo\n" +
			"(session.file, solo legible por su usuario del sistema). La sesión vence tras\n" +
			"1 hora sin uso o 12 horas desde el inicio. Tras 5 intentos fallidos la cuenta\n" +
			"se bloquea 15 minutos, y el doble en cada racha siguiente.\n\n" +
			"La contraseña se pide sin mostrarla, o se lee de la entrada estándar con\n" +
			"--password-stdin. Nunca como argumento.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), dbTimeout)
			defer cancel()
			in := newSecrets(env, passwordStdin)
			if user == "" {
				var err error
				if user, err = in.line("Usuario"); err != nil {
					return err
				}
			}
			pw, err := in.read("Contraseña", false)
			if err != nil {
				return err
			}
			s, err := openStore(ctx, env, flags, cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			defer s.close()
			res, err := s.auth.Login(ctx, user, pw)
			if err != nil {
				s.rt.logger.Warn("inicio de sesión rechazado")
				return appError(err)
			}
			if err := writeSessionFile(s.rt.loaded.Config.Session.File, res.Token); err != nil {
				return fmt.Errorf("guardar la sesión: %w", err)
			}
			s.rt.logger.Info("sesión iniciada", "user", res.User.Username.String(), "session", res.Session.ID.String())
			if flags.json {
				return writeJSON(cmd.OutOrStdout(), struct {
					Schema    string `json:"schema"`
					Username  string `json:"username"`
					ExpiresAt string `json:"expires_at"`
				}{"balanzapro.login/v1", res.User.Username.String(), rfc3339(res.Session.ExpiresAt)})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "sesión iniciada como %s (%s); vence a más tardar %s (VET)\n",
				res.User.Username, res.User.DisplayName, vet(res.Session.ExpiresAt))
			return nil
		},
	}
	login.Flags().StringVar(&user, "user", "", "usuario (si falta, se pregunta)")
	login.Flags().BoolVar(&passwordStdin, "password-stdin", false, "lee la contraseña de la entrada estándar")
	cmd.AddCommand(login)

	cmd.AddCommand(&cobra.Command{
		Use:   "logout",
		Short: "Cierra la sesión de este equipo",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), dbTimeout)
			defer cancel()
			s, err := openStore(ctx, env, flags, cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			defer s.close()
			path := s.rt.loaded.Config.Session.File
			token, err := readSessionFile(path)
			if err == nil {
				// Una sesión ya vencida o desconocida igual se borra del equipo.
				_ = s.auth.Logout(ctx, token)
			}
			if err := removeSessionFile(path); err != nil {
				return err
			}
			return writeResult(cmd, flags, "logout", "sesión cerrada")
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "whoami",
		Short: "Muestra el usuario de la sesión y sus asignaciones",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withSession(cmd, env, flags, func(ctx context.Context, s appSession) error {
				me, err := s.auth.Me(ctx, s.who)
				if err != nil {
					return appError(err)
				}
				if flags.json {
					return writeJSON(cmd.OutOrStdout(), struct {
						Schema       string   `json:"schema"`
						User         userJSON `json:"user"`
						Organization string   `json:"organization_code"`
						ExpiresAt    string   `json:"session_expires_at"`
						Permissions  []string `json:"permissions"`
					}{"balanzapro.whoami/v1", toUserJSON(me), s.cur.Organization.Code.String(), rfc3339(s.who.Session.ExpiresAt), permissionNames(s.principal())})
				}
				out := cmd.OutOrStdout()
				fmt.Fprintf(out, "%s (%s) en %s\n", me.Username, me.DisplayName, s.cur.Organization.Code)
				fmt.Fprintf(out, "sesión vence a más tardar %s (VET)\n", vet(s.who.Session.ExpiresAt))
				if len(me.Assignments) == 0 {
					fmt.Fprintln(out, "sin roles asignados")
				}
				for _, a := range me.Assignments {
					fmt.Fprintf(out, "  %s\n", describeAssignment(a))
				}
				return nil
			})
		},
	})

	var passwdStdin bool
	passwd := &cobra.Command{
		Use:   "passwd",
		Short: "Cambia su contraseña y cierra sus otras sesiones",
		Long: "Pide la contraseña actual y la nueva (dos veces). Con --password-stdin lee\n" +
			"dos líneas: la actual y la nueva.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			in := newSecrets(env, passwdStdin)
			current, err := in.read("Contraseña actual", false)
			if err != nil {
				return err
			}
			next, err := in.read("Contraseña nueva", true)
			if err != nil {
				return err
			}
			return withSession(cmd, env, flags, func(ctx context.Context, s appSession) error {
				if err := s.auth.ChangePassword(ctx, s.who, current, next); err != nil {
					return appError(err)
				}
				return writeResult(cmd, flags, "passwd", "contraseña cambiada; sus otras sesiones se cerraron")
			})
		},
	}
	passwd.Flags().BoolVar(&passwdStdin, "password-stdin", false, "lee la contraseña actual y la nueva de la entrada estándar")
	cmd.AddCommand(passwd)
	return cmd
}

// permissionNames lista los permisos efectivos con su ámbito.
func permissionNames(p identity.Principal) []string {
	out := []string{}
	for _, g := range p.Grants {
		for _, perm := range g.Permissions {
			out = append(out, string(perm)+"@"+g.Scope.Level().String())
		}
	}
	return out
}

func writeResult(cmd *cobra.Command, flags *globalFlags, action, text string) error {
	if flags.json {
		return writeJSON(cmd.OutOrStdout(), struct {
			Schema string `json:"schema"`
			Action string `json:"action"`
			OK     bool   `json:"ok"`
		}{"balanzapro.result/v1", action, true})
	}
	fmt.Fprintln(cmd.OutOrStdout(), text)
	return nil
}

func newUserCmd(env Env, flags *globalFlags) *cobra.Command {
	cmd := &cobra.Command{Use: "user", Short: "Usuarios y asignaciones de rol", Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() }}

	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "Lista los usuarios visibles y sus roles",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withSession(cmd, env, flags, func(ctx context.Context, s appSession) error {
				list, err := s.auth.ListUsers(ctx, s.principal())
				if err != nil {
					return appError(err)
				}
				if flags.json {
					items := make([]userJSON, 0, len(list))
					for _, u := range list {
						items = append(items, toUserJSON(u))
					}
					return writeJSON(cmd.OutOrStdout(), listPayload("balanzapro.users/v1", items))
				}
				tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
				fmt.Fprintln(tw, "USUARIO\tNOMBRE\tESTADO\tROLES")
				for _, u := range list {
					var roles []string
					for _, a := range u.Assignments {
						roles = append(roles, describeAssignment(a))
					}
					fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", u.Username, u.DisplayName, userStatus(u), orDash(strings.Join(roles, "; ")))
				}
				return tw.Flush()
			})
		},
	})

	var username, name string
	var createStdin bool
	create := &cobra.Command{
		Use:   "create",
		Short: "Crea un usuario sin roles; asígnelos con 'user assign'",
		Long: "La contraseña inicial se pide dos veces sin mostrarla, o se lee de la entrada\n" +
			"estándar con --password-stdin. Mínimo 8 caracteres; no puede ser común ni\n" +
			"contener el usuario.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			pw, err := newSecrets(env, createStdin).read("Contraseña inicial de "+username, true)
			if err != nil {
				return err
			}
			return withSession(cmd, env, flags, func(ctx context.Context, s appSession) error {
				u, err := s.auth.CreateUser(ctx, s.principal(), auth.NewUserInput{Username: username, DisplayName: name, Password: pw})
				if err != nil {
					return appError(err)
				}
				return writeUser(cmd, flags, auth.UserView{User: u}, "usuario creado: "+u.Username.String())
			})
		},
	}
	create.Flags().StringVar(&username, "username", "", "usuario de inicio de sesión (obligatorio)")
	create.Flags().StringVar(&name, "name", "", "nombre visible (obligatorio)")
	create.Flags().BoolVar(&createStdin, "password-stdin", false, "lee la contraseña inicial de la entrada estándar")
	markRequired(create, "username", "name")
	cmd.AddCommand(create)

	for _, spec := range []struct {
		use, short string
		assign     bool
	}{
		{"assign", "Asigna un rol en un ámbito (por defecto, toda la organización)", true},
		{"unassign", "Retira un rol asignado en un ámbito", false},
	} {
		var username, role string
		var where auth.ScopeRef
		c := &cobra.Command{
			Use:   spec.use,
			Short: spec.short,
			Long: spec.short + ".\n\nÁmbitos: sin flags, toda la organización; --company; --company y --branch;\n" +
				"o --company, --branch y --warehouse. Solo puede delegar permisos que usted\n" +
				"tiene en ese ámbito, y no puede cambiar sus propias asignaciones.",
			Args: noArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				return withSession(cmd, env, flags, func(ctx context.Context, s appSession) error {
					if !spec.assign {
						if err := s.auth.Unassign(ctx, s.principal(), username, role, where); err != nil {
							return appError(err)
						}
						return writeResult(cmd, flags, "unassign", fmt.Sprintf("rol %s retirado a %s en %s", strings.ToUpper(role), strings.ToLower(username), where))
					}
					a, err := s.auth.Assign(ctx, s.principal(), username, role, where)
					if err != nil {
						return appError(err)
					}
					if flags.json {
						return writeJSON(cmd.OutOrStdout(), struct {
							Schema   string `json:"schema"`
							Username string `json:"username"`
							assignmentJSON
						}{"balanzapro.assignment/v1", strings.ToLower(username), toAssignmentJSON(a)})
					}
					fmt.Fprintf(cmd.OutOrStdout(), "asignado: %s a %s\n", describeAssignment(a), strings.ToLower(username))
					return nil
				})
			},
		}
		c.Flags().StringVar(&username, "username", "", "usuario (obligatorio)")
		c.Flags().StringVar(&role, "role", "", "código del rol (obligatorio)")
		c.Flags().StringVar(&where.Company, "company", "", "código de empresa")
		c.Flags().StringVar(&where.Branch, "branch", "", "código de sucursal (requiere --company)")
		c.Flags().StringVar(&where.Warehouse, "warehouse", "", "código de almacén (requiere --branch)")
		markRequired(c, "username", "role")
		cmd.AddCommand(c)
	}

	for _, spec := range []struct {
		use, short, done string
		active           bool
	}{
		{"disable", "Desactiva un usuario y cierra sus sesiones", "usuario desactivado", false},
		{"enable", "Reactiva un usuario", "usuario activado", true},
	} {
		var username string
		c := &cobra.Command{
			Use:   spec.use,
			Short: spec.short,
			Args:  noArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				return withSession(cmd, env, flags, func(ctx context.Context, s appSession) error {
					u, err := s.auth.SetUserActive(ctx, s.principal(), username, spec.active)
					if err != nil {
						return appError(err)
					}
					return writeUser(cmd, flags, auth.UserView{User: u}, spec.done+": "+u.Username.String())
				})
			},
		}
		c.Flags().StringVar(&username, "username", "", "usuario (obligatorio)")
		markRequired(c, "username")
		cmd.AddCommand(c)
	}

	var unlockUser string
	unlock := &cobra.Command{
		Use:   "unlock",
		Short: "Levanta el bloqueo por intentos fallidos",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withSession(cmd, env, flags, func(ctx context.Context, s appSession) error {
				if err := s.auth.Unlock(ctx, s.principal(), unlockUser); err != nil {
					return appError(err)
				}
				return writeResult(cmd, flags, "unlock", "usuario desbloqueado: "+strings.ToLower(unlockUser))
			})
		},
	}
	unlock.Flags().StringVar(&unlockUser, "username", "", "usuario (obligatorio)")
	markRequired(unlock, "username")
	cmd.AddCommand(unlock)

	var resetUser string
	var resetStdin bool
	reset := &cobra.Command{
		Use:   "reset-password",
		Short: "Fija una contraseña nueva a otro usuario, lo desbloquea y cierra sus sesiones",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			pw, err := newSecrets(env, resetStdin).read("Contraseña nueva de "+resetUser, true)
			if err != nil {
				return err
			}
			return withSession(cmd, env, flags, func(ctx context.Context, s appSession) error {
				if err := s.auth.ResetPassword(ctx, s.principal(), resetUser, pw); err != nil {
					return appError(err)
				}
				return writeResult(cmd, flags, "reset-password", "contraseña restablecida: "+strings.ToLower(resetUser))
			})
		},
	}
	reset.Flags().StringVar(&resetUser, "username", "", "usuario (obligatorio)")
	reset.Flags().BoolVar(&resetStdin, "password-stdin", false, "lee la contraseña nueva de la entrada estándar")
	markRequired(reset, "username")
	cmd.AddCommand(reset)
	return cmd
}

func writeUser(cmd *cobra.Command, flags *globalFlags, v auth.UserView, text string) error {
	if flags.json {
		return writeJSON(cmd.OutOrStdout(), struct {
			Schema string `json:"schema"`
			userJSON
		}{"balanzapro.user/v1", toUserJSON(v)})
	}
	fmt.Fprintln(cmd.OutOrStdout(), text)
	return nil
}

func userStatus(u auth.UserView) string {
	switch {
	case u.Status != identity.StatusActive:
		return "desactivado"
	case u.Locked:
		return "bloqueado"
	default:
		return "activo"
	}
}

type roleJSON struct {
	ID          string   `json:"id"`
	Code        string   `json:"code"`
	Name        string   `json:"name"`
	Permissions []string `json:"permissions"`
}

func toRoleJSON(r identity.Role) roleJSON {
	out := roleJSON{ID: r.ID.String(), Code: r.Code.String(), Name: r.Name.String(), Permissions: []string{}}
	for _, p := range r.Permissions {
		out.Permissions = append(out.Permissions, string(p))
	}
	return out
}

func newRoleCmd(env Env, flags *globalFlags) *cobra.Command {
	cmd := &cobra.Command{Use: "role", Short: "Roles y permisos de la organización", Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() }}

	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "Lista los roles y sus permisos",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withSession(cmd, env, flags, func(ctx context.Context, s appSession) error {
				list, err := s.auth.ListRoles(ctx, s.principal())
				if err != nil {
					return appError(err)
				}
				if flags.json {
					items := make([]roleJSON, 0, len(list))
					for _, r := range list {
						items = append(items, toRoleJSON(r))
					}
					return writeJSON(cmd.OutOrStdout(), listPayload("balanzapro.roles/v1", items))
				}
				tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
				fmt.Fprintln(tw, "CÓDIGO\tNOMBRE\tPERMISOS")
				for _, r := range list {
					fmt.Fprintf(tw, "%s\t%s\t%s\n", r.Code, r.Name, orDash(strings.Join(toRoleJSON(r).Permissions, ", ")))
				}
				return tw.Flush()
			})
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "permissions",
		Short: "Muestra el catálogo de permisos",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			catalog := identity.Catalog()
			if flags.json {
				type item struct {
					Permission    string `json:"permission"`
					Description   string `json:"description"`
					Extraordinary bool   `json:"extraordinary"`
				}
				items := make([]item, 0, len(catalog))
				for _, p := range catalog {
					items = append(items, item{string(p.Permission), p.Description, p.Extraordinary})
				}
				return writeJSON(cmd.OutOrStdout(), listPayload("balanzapro.permissions/v1", items))
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "PERMISO\tDESCRIPCIÓN")
			for _, p := range catalog {
				desc := p.Description
				if p.Extraordinary {
					desc += " [extraordinario]"
				}
				fmt.Fprintf(tw, "%s\t%s\n", p.Permission, desc)
			}
			return tw.Flush()
		},
	})

	var code, name string
	var perms []string
	create := &cobra.Command{
		Use:   "create",
		Short: "Crea un rol",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withSession(cmd, env, flags, func(ctx context.Context, s appSession) error {
				r, err := s.auth.CreateRole(ctx, s.principal(), code, name, perms)
				if err != nil {
					return appError(err)
				}
				return writeRole(cmd, flags, r, "rol creado")
			})
		},
	}
	create.Flags().StringVar(&code, "code", "", "código único (obligatorio)")
	create.Flags().StringVar(&name, "name", "", "nombre (obligatorio)")
	create.Flags().StringSliceVar(&perms, "permission", nil, "permiso (repetible o separado por comas)")
	markRequired(create, "code", "name")
	cmd.AddCommand(create)

	var editCode string
	var add, remove []string
	edit := &cobra.Command{
		Use:   "edit",
		Short: "Agrega o quita permisos de un rol (afecta a todos sus usuarios)",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if len(add)+len(remove) == 0 {
				return usageErrorf("indique --add o --remove")
			}
			return withSession(cmd, env, flags, func(ctx context.Context, s appSession) error {
				r, err := s.auth.EditRole(ctx, s.principal(), editCode, add, remove)
				if err != nil {
					return appError(err)
				}
				return writeRole(cmd, flags, r, "rol actualizado")
			})
		},
	}
	edit.Flags().StringVar(&editCode, "code", "", "código del rol (obligatorio)")
	edit.Flags().StringSliceVar(&add, "add", nil, "permiso a agregar (repetible)")
	edit.Flags().StringSliceVar(&remove, "remove", nil, "permiso a quitar (repetible)")
	markRequired(edit, "code")
	cmd.AddCommand(edit)
	return cmd
}

func writeRole(cmd *cobra.Command, flags *globalFlags, r identity.Role, verb string) error {
	if flags.json {
		return writeJSON(cmd.OutOrStdout(), struct {
			Schema string `json:"schema"`
			roleJSON
		}{"balanzapro.role/v1", toRoleJSON(r)})
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s: %s (%s)\n", verb, r.Code, orDash(strings.Join(toRoleJSON(r).Permissions, ", ")))
	return nil
}
