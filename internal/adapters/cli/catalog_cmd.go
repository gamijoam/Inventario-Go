package cli

import (
	"context"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	appcatalog "github.com/gamijoam/inventario-go/internal/application/catalog"
	"github.com/gamijoam/inventario-go/internal/domain/catalog"
)

type unitJSON struct {
	ID    string `json:"id"`
	Code  string `json:"code"`
	Name  string `json:"name"`
	Scale int32  `json:"scale"`
}

func toUnitJSON(u catalog.Unit) unitJSON {
	return unitJSON{u.ID.String(), u.Code, u.Name.String(), u.Scale}
}

type productJSON struct {
	ID        string   `json:"id"`
	SKU       string   `json:"sku"`
	Name      string   `json:"name"`
	Unit      string   `json:"unit"`
	UnitScale int32    `json:"unit_scale"`
	Barcodes  []string `json:"barcodes"`
	Status    string   `json:"status"`
	Revision  int64    `json:"revision"`
	CreatedAt string   `json:"created_at"`
	UpdatedAt string   `json:"updated_at"`
}

func toProductJSON(p appcatalog.ProductView) productJSON {
	out := productJSON{ID: p.ID.String(), SKU: p.SKU.String(), Name: p.Name.String(), Unit: p.Unit.Code, UnitScale: p.Unit.Scale,
		Barcodes: []string{}, Status: string(p.Status), Revision: p.Revision, CreatedAt: rfc3339(p.CreatedAt), UpdatedAt: rfc3339(p.UpdatedAt)}
	for _, b := range p.Barcodes {
		out.Barcodes = append(out.Barcodes, b.String())
	}
	return out
}

func productStatus(s catalog.Status) string {
	if s == catalog.StatusActive {
		return "activo"
	}
	return "inactivo"
}

func newUnitCmd(env Env, flags *globalFlags) *cobra.Command {
	cmd := &cobra.Command{Use: "unit", Short: "Unidades de medida del catálogo", Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() }}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "Lista las unidades y sus decimales",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withSession(cmd, env, flags, func(ctx context.Context, s appSession) error {
				list, err := s.cat.ListUnits(ctx, s.principal())
				if err != nil {
					return appError(err)
				}
				if flags.json {
					items := make([]unitJSON, 0, len(list))
					for _, u := range list {
						items = append(items, toUnitJSON(u))
					}
					return writeJSON(cmd.OutOrStdout(), listPayload("balanzapro.units/v1", items))
				}
				tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
				fmt.Fprintln(tw, "CÓDIGO\tNOMBRE\tDECIMALES")
				for _, u := range list {
					fmt.Fprintf(tw, "%s\t%s\t%d\n", u.Code, u.Name, u.Scale)
				}
				return tw.Flush()
			})
		},
	})
	var code, name string
	var scale int32
	create := &cobra.Command{
		Use:   "create",
		Short: "Crea una unidad de medida",
		Long: "Crea una unidad con la cantidad de decimales que admite (0 a 6), p. ej.\n" +
			"LB con 3 para vender por peso. Los decimales no se pueden cambiar después,\n" +
			"porque las cantidades registradas dependen de ellos.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withSession(cmd, env, flags, func(ctx context.Context, s appSession) error {
				u, err := s.cat.CreateUnit(ctx, s.principal(), s.cur.Organization, code, name, scale)
				if err != nil {
					return appError(err)
				}
				if flags.json {
					return writeJSON(cmd.OutOrStdout(), struct {
						Schema string `json:"schema"`
						unitJSON
					}{"balanzapro.unit/v1", toUnitJSON(u)})
				}
				fmt.Fprintf(cmd.OutOrStdout(), "unidad creada: %s %s (%d decimales)\n", u.Code, u.Name, u.Scale)
				return nil
			})
		},
	}
	create.Flags().StringVar(&code, "code", "", "código, p. ej. LB (obligatorio)")
	create.Flags().StringVar(&name, "name", "", "nombre (obligatorio)")
	create.Flags().Int32Var(&scale, "decimals", 0, "decimales admitidos, de 0 a 6")
	markRequired(create, "code", "name")
	cmd.AddCommand(create)
	return cmd
}

func newProductCmd(env Env, flags *globalFlags) *cobra.Command {
	cmd := &cobra.Command{Use: "product", Short: "Productos del catálogo", Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() }}

	var in appcatalog.NewProduct
	create := &cobra.Command{
		Use:   "create",
		Short: "Crea un producto simple",
		Long: "Solo el nombre es obligatorio. Sin --sku se asigna P-000001, P-000002…;\n" +
			"sin --unit, UND. Los códigos de barras EAN/UPC se validan con su dígito\n" +
			"verificador. La unidad no se puede cambiar después.\n\n" +
			"Ejemplo:\n  balanzapro product create --name \"Harina de maíz 1 kg\" --barcode 7591234567894",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withSession(cmd, env, flags, func(ctx context.Context, s appSession) error {
				p, err := s.cat.CreateProduct(ctx, s.principal(), s.cur.Organization, in)
				if err != nil {
					return appError(err)
				}
				return writeProduct(cmd, flags, p, "producto creado")
			})
		},
	}
	create.Flags().StringVar(&in.Name, "name", "", "nombre (obligatorio)")
	create.Flags().StringVar(&in.SKU, "sku", "", "referencia comercial (por defecto, automática)")
	create.Flags().StringVar(&in.UnitCode, "unit", "", "unidad base (por defecto, UND)")
	create.Flags().StringSliceVar(&in.Barcodes, "barcode", nil, "código de barras (repetible)")
	markRequired(create, "name")
	cmd.AddCommand(create)

	var f appcatalog.Filter
	list := &cobra.Command{
		Use:   "list",
		Short: "Lista productos; --search busca por nombre, SKU o código de barras",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withSession(cmd, env, flags, func(ctx context.Context, s appSession) error {
				list, err := s.cat.ListProducts(ctx, s.principal(), f)
				if err != nil {
					return appError(err)
				}
				if flags.json {
					items := make([]productJSON, 0, len(list))
					for _, p := range list {
						items = append(items, toProductJSON(p))
					}
					return writeJSON(cmd.OutOrStdout(), listPayload("balanzapro.products/v1", items))
				}
				tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
				fmt.Fprintln(tw, "SKU\tNOMBRE\tUNIDAD\tCÓDIGOS\tESTADO")
				for _, p := range list {
					fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", p.SKU, p.Name, p.Unit.Code, orDash(strings.Join(toProductJSON(p).Barcodes, ", ")), productStatus(p.Status))
				}
				if err := tw.Flush(); err != nil {
					return err
				}
				if len(list) == f.Limit {
					fmt.Fprintf(cmd.ErrOrStderr(), "se muestran %d; use --offset %d para ver más\n", f.Limit, f.Offset+f.Limit)
				}
				return nil
			})
		},
	}
	list.Flags().StringVar(&f.Search, "search", "", "texto a buscar")
	list.Flags().BoolVar(&f.IncludeInactive, "all", false, "incluye productos inactivos")
	list.Flags().IntVar(&f.Limit, "limit", 50, fmt.Sprintf("máximo de resultados (1 a %d)", appcatalog.MaxListLimit))
	list.Flags().IntVar(&f.Offset, "offset", 0, "resultados a saltar")
	cmd.AddCommand(list)

	cmd.AddCommand(&cobra.Command{
		Use:   "show REFERENCIA",
		Short: "Muestra un producto por SKU, código de barras o ID",
		Args:  exactArgs(1, "indique el SKU, el código de barras o el ID del producto"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withSession(cmd, env, flags, func(ctx context.Context, s appSession) error {
				p, err := s.cat.ShowProduct(ctx, s.principal(), args[0])
				if err != nil {
					return appError(err)
				}
				return writeProduct(cmd, flags, p, "")
			})
		},
	})

	var name, sku string
	var add, remove []string
	var activate, deactivate bool
	var revision int64
	update := &cobra.Command{
		Use:   "update REFERENCIA",
		Short: "Modifica nombre, SKU, códigos de barras o estado de un producto",
		Long: "Los productos no se borran: se inactivan con --inactive y se reactivan con\n" +
			"--active. Con --revision N el cambio se rechaza (código 6) si alguien modificó\n" +
			"el producto después de la revisión N.",
		Args: exactArgs(1, "indique el SKU, el código de barras o el ID del producto"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if activate && deactivate {
				return usageErrorf("--active y --inactive se excluyen")
			}
			c := appcatalog.Changes{AddBarcodes: add, RemoveBarcodes: remove, ExpectedRevision: revision}
			if cmd.Flags().Changed("name") {
				c.Name = &name
			}
			if cmd.Flags().Changed("sku") {
				c.SKU = &sku
			}
			if activate || deactivate {
				c.Active = &activate
			}
			return withSession(cmd, env, flags, func(ctx context.Context, s appSession) error {
				p, err := s.cat.UpdateProduct(ctx, s.principal(), s.cur.Organization, args[0], c)
				if err != nil {
					return appError(err)
				}
				return writeProduct(cmd, flags, p, "producto actualizado")
			})
		},
	}
	update.Flags().StringVar(&name, "name", "", "nombre nuevo")
	update.Flags().StringVar(&sku, "sku", "", "SKU nuevo")
	update.Flags().StringSliceVar(&add, "add-barcode", nil, "código de barras a agregar (repetible)")
	update.Flags().StringSliceVar(&remove, "remove-barcode", nil, "código de barras a quitar (repetible)")
	update.Flags().BoolVar(&activate, "active", false, "reactiva el producto")
	update.Flags().BoolVar(&deactivate, "inactive", false, "inactiva el producto")
	update.Flags().Int64Var(&revision, "revision", 0, "revisión esperada (control de ediciones simultáneas)")
	cmd.AddCommand(update)
	return cmd
}

func exactArgs(n int, msg string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) != n {
			return usageErrorf("%s", msg)
		}
		return nil
	}
}

func writeProduct(cmd *cobra.Command, flags *globalFlags, p appcatalog.ProductView, verb string) error {
	if flags.json {
		return writeJSON(cmd.OutOrStdout(), struct {
			Schema string `json:"schema"`
			productJSON
		}{"balanzapro.product/v1", toProductJSON(p)})
	}
	out := cmd.OutOrStdout()
	if verb != "" {
		fmt.Fprintf(out, "%s: %s %s\n", verb, p.SKU, p.Name)
		return nil
	}
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "SKU\t%s\n", p.SKU)
	fmt.Fprintf(tw, "nombre\t%s\n", p.Name)
	fmt.Fprintf(tw, "unidad\t%s (%s, %d decimales)\n", p.Unit.Code, p.Unit.Name, p.Unit.Scale)
	fmt.Fprintf(tw, "códigos\t%s\n", orDash(strings.Join(toProductJSON(p).Barcodes, ", ")))
	fmt.Fprintf(tw, "estado\t%s\n", productStatus(p.Status))
	fmt.Fprintf(tw, "revisión\t%d (actualizado %s VET)\n", p.Revision, vet(p.UpdatedAt))
	fmt.Fprintf(tw, "ID\t%s\n", p.ID)
	return tw.Flush()
}
