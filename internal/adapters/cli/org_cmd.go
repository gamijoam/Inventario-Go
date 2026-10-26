package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"
	"unicode"

	"github.com/spf13/cobra"

	"github.com/gamijoam/inventario-go/internal/adapters/persistence"
	apporg "github.com/gamijoam/inventario-go/internal/application/org"
	"github.com/gamijoam/inventario-go/internal/domain/org"
	"github.com/gamijoam/inventario-go/internal/domain/timezone"
)

type orgJSON struct {
	ID        string `json:"id"`
	Code      string `json:"code"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
}

type companyJSON struct {
	ID             string `json:"id"`
	OrganizationID string `json:"organization_id"`
	Code           string `json:"code"`
	LegalName      string `json:"legal_name"`
	RIF            string `json:"rif"`
	Status         string `json:"status"`
	CreatedAt      string `json:"created_at"`
}

type branchJSON struct {
	ID             string `json:"id"`
	OrganizationID string `json:"organization_id"`
	CompanyID      string `json:"company_id"`
	CompanyCode    string `json:"company_code"`
	Code           string `json:"code"`
	Name           string `json:"name"`
	Status         string `json:"status"`
	CreatedAt      string `json:"created_at"`
}

type warehouseJSON struct {
	ID             string `json:"id"`
	OrganizationID string `json:"organization_id"`
	CompanyID      string `json:"company_id"`
	BranchID       string `json:"branch_id"`
	Code           string `json:"code"`
	Name           string `json:"name"`
	Status         string `json:"status"`
	CreatedAt      string `json:"created_at"`
}

func toOrgJSON(o org.Organization) orgJSON {
	return orgJSON{o.ID.String(), o.Code.String(), o.Name.String(), string(o.Status), rfc3339(o.CreatedAt)}
}

func toCompanyJSON(c org.Company) companyJSON {
	return companyJSON{c.ID.String(), c.OrganizationID.String(), c.Code.String(), c.LegalName.String(), c.RIF.String(), string(c.Status), rfc3339(c.CreatedAt)}
}

func toBranchJSON(b org.Branch, companyCode string) branchJSON {
	return branchJSON{b.ID.String(), b.OrganizationID.String(), b.CompanyID.String(), companyCode, b.Code.String(), b.Name.String(), string(b.Status), rfc3339(b.CreatedAt)}
}

func toWarehouseJSON(w org.Warehouse) warehouseJSON {
	return warehouseJSON{w.ID.String(), w.OrganizationID.String(), w.CompanyID.String(), w.BranchID.String(), w.Code.String(), w.Name.String(), string(w.Status), rfc3339(w.CreatedAt)}
}

func rfc3339(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// orgSession abre la base existente y resuelve el contexto de la
// instalación. Hasta BP-M1-002 la CLI local actúa con el ámbito de la
// organización asociada a la instalación, nunca con IDs que reciba.
type orgSession struct {
	svc   *apporg.Service
	cur   apporg.Context
	close func()
}

func openOrgSession(ctx context.Context, env Env, flags *globalFlags, errOut io.Writer) (orgSession, error) {
	rt, err := loadRuntime(env, flags, errOut)
	if err != nil {
		return orgSession{}, err
	}
	store, err := persistence.Open(ctx, rt.loaded.Config.Database, false)
	if errors.Is(err, persistence.ErrNotInitialized) {
		rt.close()
		return orgSession{}, orgError(apporg.ErrNotBootstrapped)
	}
	if err != nil {
		rt.close()
		return orgSession{}, unavailableError(err)
	}
	closeAll := func() { _ = store.Close(); _ = rt.close() }
	if err := requireSchema(ctx, store); err != nil {
		closeAll()
		return orgSession{}, err
	}
	svc := apporg.NewService(store.OrgStore(), func() time.Time { return time.Now().UTC() })
	cur, err := svc.Current(ctx)
	if err != nil {
		closeAll()
		return orgSession{}, orgError(err)
	}
	return orgSession{svc: svc, cur: cur, close: closeAll}, nil
}

// requireSchema evita operar sobre una base con migraciones pendientes.
func requireSchema(ctx context.Context, store *persistence.Store) error {
	runner, err := store.Runner()
	if err != nil {
		return err
	}
	st, err := runner.Status(ctx)
	if err != nil {
		return unavailableError(err)
	}
	if err := st.Problem(); err != nil {
		return err
	}
	if len(st.Pending) > 0 {
		return fmt.Errorf("la base tiene %d migraciones pendientes; ejecute 'balanzapro db migrate'", len(st.Pending))
	}
	return nil
}

// orgError traduce errores de la capa de aplicación a códigos de salida.
func orgError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, apporg.ErrConflict), errors.Is(err, apporg.ErrAlreadyBootstrapped):
		return &exitError{code: ExitConflict, kind: "conflict", msg: err.Error()}
	case errors.Is(err, apporg.ErrNotFound), errors.Is(err, apporg.ErrNotBootstrapped):
		return &exitError{code: ExitNotFound, kind: "not_found", msg: err.Error()}
	case errors.Is(err, org.ErrInvalid):
		return usageErrorf("%s", err)
	default:
		return err
	}
}

func newSetupCmd(env Env, flags *globalFlags) *cobra.Command {
	var in apporg.BootstrapInput
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Configura una instalación nueva: organización, empresa, sucursal y almacén",
		Long: "Crea la base si hace falta, aplica las migraciones y registra en una sola\n" +
			"transacción la organización, su empresa legal, la sucursal y el almacén\n" +
			"principal, asociados a esta instalación. Solo se puede ejecutar una vez.\n\n" +
			"Ejemplo:\n  balanzapro setup --name \"Bodega Ana\" --legal-name \"Bodega Ana, C.A.\" --rif J-12345678-9",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(in.OrganizationName) == "" {
				return usageErrorf("--name es obligatorio")
			}
			if in.OrganizationCode == "" {
				in.OrganizationCode = codeFromName(in.OrganizationName)
			}
			if in.CompanyLegalName == "" {
				in.CompanyLegalName = in.OrganizationName
			}
			return runSetup(cmd, env, flags, in)
		},
	}
	f := cmd.Flags()
	f.StringVar(&in.OrganizationName, "name", "", "nombre del negocio (obligatorio)")
	f.StringVar(&in.OrganizationCode, "code", "", "código de la organización (por defecto, derivado del nombre)")
	f.StringVar(&in.CompanyLegalName, "legal-name", "", "razón social de la empresa (por defecto, el nombre)")
	f.StringVar(&in.RIF, "rif", "", "RIF de la empresa, p. ej. J-12345678-9 (opcional)")
	f.StringVar(&in.CompanyCode, "company-code", "PRINCIPAL", "código de la empresa")
	f.StringVar(&in.BranchName, "branch-name", "Principal", "nombre de la sucursal")
	f.StringVar(&in.BranchCode, "branch-code", "PRINCIPAL", "código de la sucursal")
	f.StringVar(&in.WarehouseName, "warehouse-name", "Principal", "nombre del almacén")
	f.StringVar(&in.WarehouseCode, "warehouse-code", "PRINCIPAL", "código del almacén")
	return cmd
}

func runSetup(cmd *cobra.Command, env Env, flags *globalFlags, in apporg.BootstrapInput) error {
	rt, err := loadRuntime(env, flags, cmd.ErrOrStderr())
	if err != nil {
		return err
	}
	defer rt.close()
	ctx, cancel := context.WithTimeout(cmd.Context(), dbTimeout)
	defer cancel()

	store, err := persistence.Open(ctx, rt.loaded.Config.Database, true)
	if err != nil {
		return unavailableError(err)
	}
	defer store.Close()
	runner, err := store.Runner()
	if err != nil {
		return err
	}
	if _, err := runner.Up(ctx); err != nil {
		return err
	}
	if _, _, err := store.EnsureInstallation(ctx); err != nil {
		return err
	}
	svc := apporg.NewService(store.OrgStore(), func() time.Time { return time.Now().UTC() })
	res, err := svc.Bootstrap(ctx, in)
	if err != nil {
		return orgError(err)
	}
	rt.logger.Info("instalación configurada", "organization", res.Organization.Code.String(), "branch", res.Branch.Code.String())

	if flags.json {
		return writeJSON(cmd.OutOrStdout(), struct {
			Schema       string        `json:"schema"`
			Organization orgJSON       `json:"organization"`
			Company      companyJSON   `json:"company"`
			Branch       branchJSON    `json:"branch"`
			Warehouse    warehouseJSON `json:"warehouse"`
		}{"balanzapro.setup/v1", toOrgJSON(res.Organization), toCompanyJSON(res.Company),
			toBranchJSON(res.Branch, res.Company.Code.String()), toWarehouseJSON(res.Warehouse)})
	}
	out := cmd.OutOrStdout()
	fmt.Fprintln(out, "instalación configurada:")
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "  organización\t%s\t%s\n", res.Organization.Code, res.Organization.Name)
	fmt.Fprintf(tw, "  empresa\t%s\t%s%s\n", res.Company.Code, res.Company.LegalName, rifSuffix(res.Company.RIF))
	fmt.Fprintf(tw, "  sucursal\t%s\t%s\n", res.Branch.Code, res.Branch.Name)
	fmt.Fprintf(tw, "  almacén\t%s\t%s\n", res.Warehouse.Code, res.Warehouse.Name)
	return tw.Flush()
}

func newOrgCmd(env Env, flags *globalFlags) *cobra.Command {
	cmd := &cobra.Command{Use: "org", Short: "Organización de esta instalación", Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() }}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "Muestra la organización asociada y la sucursal activa",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withOrg(cmd, env, flags, func(ctx context.Context, s orgSession) error {
				c := s.cur
				if flags.json {
					return writeJSON(cmd.OutOrStdout(), struct {
						Schema string    `json:"schema"`
						Items  []orgJSON `json:"items"`
						Active struct {
							CompanyCode string `json:"company_code"`
							BranchCode  string `json:"branch_code"`
						} `json:"active"`
					}{Schema: "balanzapro.organizations/v1", Items: []orgJSON{toOrgJSON(c.Organization)},
						Active: struct {
							CompanyCode string `json:"company_code"`
							BranchCode  string `json:"branch_code"`
						}{c.Company.Code.String(), c.Branch.Code.String()}})
				}
				tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
				fmt.Fprintln(tw, "CÓDIGO\tNOMBRE\tESTADO\tCREADA (VET)")
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", c.Organization.Code, c.Organization.Name, statusText(c.Organization.Status), vet(c.Organization.CreatedAt))
				fmt.Fprintf(tw, "\nactiva: empresa %s, sucursal %s\n", c.Company.Code, c.Branch.Code)
				return tw.Flush()
			})
		},
	})
	return cmd
}

func newCompanyCmd(env Env, flags *globalFlags) *cobra.Command {
	cmd := &cobra.Command{Use: "company", Short: "Empresas legales de la organización", Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() }}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "Lista las empresas",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withOrg(cmd, env, flags, func(ctx context.Context, s orgSession) error {
				list, err := s.svc.ListCompanies(ctx, s.cur.Scope())
				if err != nil {
					return orgError(err)
				}
				if flags.json {
					items := make([]companyJSON, 0, len(list))
					for _, c := range list {
						items = append(items, toCompanyJSON(c))
					}
					return writeJSON(cmd.OutOrStdout(), listPayload("balanzapro.companies/v1", items))
				}
				tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
				fmt.Fprintln(tw, "CÓDIGO\tRAZÓN SOCIAL\tRIF\tESTADO")
				for _, c := range list {
					fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", c.Code, c.LegalName, orDash(c.RIF.String()), statusText(c.Status))
				}
				return tw.Flush()
			})
		},
	})
	var code, legalName, rif string
	create := &cobra.Command{
		Use:   "create",
		Short: "Crea una empresa legal",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withOrg(cmd, env, flags, func(ctx context.Context, s orgSession) error {
				c, err := s.svc.CreateCompany(ctx, s.cur.Scope(), code, legalName, rif)
				if err != nil {
					return orgError(err)
				}
				if flags.json {
					return writeJSON(cmd.OutOrStdout(), struct {
						Schema string `json:"schema"`
						companyJSON
					}{"balanzapro.company/v1", toCompanyJSON(c)})
				}
				fmt.Fprintf(cmd.OutOrStdout(), "empresa creada: %s %s%s\n", c.Code, c.LegalName, rifSuffix(c.RIF))
				return nil
			})
		},
	}
	create.Flags().StringVar(&code, "code", "", "código único en la organización (obligatorio)")
	create.Flags().StringVar(&legalName, "legal-name", "", "razón social (obligatoria)")
	create.Flags().StringVar(&rif, "rif", "", "RIF, p. ej. J-12345678-9 (opcional)")
	markRequired(create, "code", "legal-name")
	cmd.AddCommand(create)
	return cmd
}

func newBranchCmd(env Env, flags *globalFlags) *cobra.Command {
	cmd := &cobra.Command{Use: "branch", Short: "Sucursales", Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() }}
	var listCompany string
	list := &cobra.Command{
		Use:   "list",
		Short: "Lista las sucursales (de todas las empresas o de --company)",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withOrg(cmd, env, flags, func(ctx context.Context, s orgSession) error {
				list, err := s.svc.ListBranches(ctx, s.cur.Scope(), listCompany)
				if err != nil {
					return orgError(err)
				}
				codes, err := companyCodes(ctx, s)
				if err != nil {
					return err
				}
				if flags.json {
					items := make([]branchJSON, 0, len(list))
					for _, b := range list {
						items = append(items, toBranchJSON(b, codes[b.CompanyID.String()]))
					}
					return writeJSON(cmd.OutOrStdout(), listPayload("balanzapro.branches/v1", items))
				}
				tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
				fmt.Fprintln(tw, "EMPRESA\tCÓDIGO\tNOMBRE\tESTADO\t")
				for _, b := range list {
					active := ""
					if b.ID == s.cur.Branch.ID {
						active = "(esta instalación)"
					}
					fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", codes[b.CompanyID.String()], b.Code, b.Name, statusText(b.Status), active)
				}
				return tw.Flush()
			})
		},
	}
	list.Flags().StringVar(&listCompany, "company", "", "código de empresa")
	cmd.AddCommand(list)

	var company, code, name string
	create := &cobra.Command{
		Use:   "create",
		Short: "Crea una sucursal en una empresa (por defecto, la de esta instalación)",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withOrg(cmd, env, flags, func(ctx context.Context, s orgSession) error {
				if company == "" {
					company = s.cur.Company.Code.String()
				}
				b, err := s.svc.CreateBranch(ctx, s.cur.Scope(), company, code, name)
				if err != nil {
					return orgError(err)
				}
				if flags.json {
					return writeJSON(cmd.OutOrStdout(), struct {
						Schema string `json:"schema"`
						branchJSON
					}{"balanzapro.branch/v1", toBranchJSON(b, strings.ToUpper(company))})
				}
				fmt.Fprintf(cmd.OutOrStdout(), "sucursal creada: %s %s (empresa %s)\n", b.Code, b.Name, strings.ToUpper(company))
				return nil
			})
		},
	}
	create.Flags().StringVar(&company, "company", "", "código de empresa (por defecto, la de esta instalación)")
	create.Flags().StringVar(&code, "code", "", "código único en la empresa (obligatorio)")
	create.Flags().StringVar(&name, "name", "", "nombre (obligatorio)")
	markRequired(create, "code", "name")
	cmd.AddCommand(create)
	return cmd
}

func newWarehouseCmd(env Env, flags *globalFlags) *cobra.Command {
	cmd := &cobra.Command{Use: "warehouse", Short: "Almacenes", Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() }}
	var listCompany, listBranch string
	list := &cobra.Command{
		Use:   "list",
		Short: "Lista los almacenes (filtros opcionales --company y --branch)",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if listBranch != "" && listCompany == "" {
				return usageErrorf("--branch requiere --company")
			}
			return withOrg(cmd, env, flags, func(ctx context.Context, s orgSession) error {
				list, err := s.svc.ListWarehouses(ctx, s.cur.Scope(), listCompany, listBranch)
				if err != nil {
					return orgError(err)
				}
				if flags.json {
					items := make([]warehouseJSON, 0, len(list))
					for _, w := range list {
						items = append(items, toWarehouseJSON(w))
					}
					return writeJSON(cmd.OutOrStdout(), listPayload("balanzapro.warehouses/v1", items))
				}
				branches, err := s.svc.ListBranches(ctx, s.cur.Scope(), "")
				if err != nil {
					return orgError(err)
				}
				branchCode := map[string]string{}
				for _, b := range branches {
					branchCode[b.ID.String()] = b.Code.String()
				}
				tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
				fmt.Fprintln(tw, "SUCURSAL\tCÓDIGO\tNOMBRE\tESTADO")
				for _, w := range list {
					fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", branchCode[w.BranchID.String()], w.Code, w.Name, statusText(w.Status))
				}
				return tw.Flush()
			})
		},
	}
	list.Flags().StringVar(&listCompany, "company", "", "código de empresa")
	list.Flags().StringVar(&listBranch, "branch", "", "código de sucursal (requiere --company)")
	cmd.AddCommand(list)

	var company, branch, code, name string
	create := &cobra.Command{
		Use:   "create",
		Short: "Crea un almacén (por defecto, en la sucursal de esta instalación)",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if (company == "") != (branch == "") {
				return usageErrorf("indique --company y --branch juntos, o ninguno para usar la sucursal de esta instalación")
			}
			return withOrg(cmd, env, flags, func(ctx context.Context, s orgSession) error {
				if company == "" {
					company, branch = s.cur.Company.Code.String(), s.cur.Branch.Code.String()
				}
				w, err := s.svc.CreateWarehouse(ctx, s.cur.Scope(), company, branch, code, name)
				if err != nil {
					return orgError(err)
				}
				if flags.json {
					return writeJSON(cmd.OutOrStdout(), struct {
						Schema string `json:"schema"`
						warehouseJSON
					}{"balanzapro.warehouse/v1", toWarehouseJSON(w)})
				}
				fmt.Fprintf(cmd.OutOrStdout(), "almacén creado: %s %s (sucursal %s)\n", w.Code, w.Name, strings.ToUpper(branch))
				return nil
			})
		},
	}
	create.Flags().StringVar(&company, "company", "", "código de empresa")
	create.Flags().StringVar(&branch, "branch", "", "código de sucursal")
	create.Flags().StringVar(&code, "code", "", "código único en la sucursal (obligatorio)")
	create.Flags().StringVar(&name, "name", "", "nombre (obligatorio)")
	markRequired(create, "code", "name")
	cmd.AddCommand(create)
	return cmd
}

func withOrg(cmd *cobra.Command, env Env, flags *globalFlags, fn func(context.Context, orgSession) error) error {
	ctx, cancel := context.WithTimeout(cmd.Context(), dbTimeout)
	defer cancel()
	s, err := openOrgSession(ctx, env, flags, cmd.ErrOrStderr())
	if err != nil {
		return err
	}
	defer s.close()
	return fn(ctx, s)
}

func companyCodes(ctx context.Context, s orgSession) (map[string]string, error) {
	list, err := s.svc.ListCompanies(ctx, s.cur.Scope())
	if err != nil {
		return nil, orgError(err)
	}
	out := make(map[string]string, len(list))
	for _, c := range list {
		out[c.ID.String()] = c.Code.String()
	}
	return out, nil
}

func listPayload[T any](schema string, items []T) any {
	return struct {
		Schema string `json:"schema"`
		Items  []T    `json:"items"`
	}{schema, items}
}

func markRequired(cmd *cobra.Command, names ...string) {
	for _, n := range names {
		_ = cmd.MarkFlagRequired(n)
	}
}

func statusText(s org.Status) string {
	if s == org.StatusActive {
		return "activa"
	}
	return "inactiva"
}

func rifSuffix(r org.RIF) string {
	if r.IsZero() {
		return ""
	}
	return " (RIF " + r.String() + ")"
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func vet(t time.Time) string { return timezone.InCaracas(t).Format("2006-01-02 15:04") }

var accents = strings.NewReplacer("Á", "A", "É", "E", "Í", "I", "Ó", "O", "Ú", "U", "Ü", "U", "Ñ", "N")

// codeFromName deriva un código válido: "Panadería Ñandú" → "PANADERIA-NANDU".
func codeFromName(name string) string {
	upper := accents.Replace(strings.ToUpper(name))
	var b strings.Builder
	dash := false
	for _, r := range upper {
		switch {
		case r < unicode.MaxASCII && (unicode.IsUpper(r) || unicode.IsDigit(r)):
			b.WriteRune(r)
			dash = false
		case b.Len() > 0 && !dash:
			b.WriteByte('-')
			dash = true
		}
	}
	code := strings.TrimRight(b.String(), "-")
	if len(code) > 32 {
		code = strings.TrimRight(code[:32], "-")
	}
	if code == "" {
		return "PRINCIPAL"
	}
	return code
}
