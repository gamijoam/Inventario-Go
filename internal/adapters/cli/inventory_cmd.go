package cli

import (
	"context"
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/gamijoam/inventario-go/internal/application/stock"
	"github.com/gamijoam/inventario-go/internal/domain/inventory"
)

type balanceJSON struct {
	Company   string `json:"company_code"`
	Branch    string `json:"branch_code"`
	Warehouse string `json:"warehouse_code"`
	ProductID string `json:"product_id"`
	SKU       string `json:"sku"`
	Name      string `json:"name"`
	Unit      string `json:"unit"`
	OnHand    string `json:"on_hand"`
	Reserved  string `json:"reserved"`
	Blocked   string `json:"blocked"`
	Available string `json:"available"`
	Version   int64  `json:"version"`
}

func toBalanceJSON(b stock.BalanceView) balanceJSON {
	q := func(v interface{ StringFixed(int32) string }) string { return v.StringFixed(b.Unit.Scale) }
	return balanceJSON{b.Location.Company.Code.String(), b.Location.Branch.Code.String(), b.Location.Warehouse.Code.String(),
		b.ProductID.String(), b.Product.SKU.String(), b.Product.Name.String(), b.Unit.Code,
		q(b.OnHand), q(b.Reserved), q(b.Blocked), q(b.Available()), b.Version}
}

type movementJSON struct {
	ID          string `json:"id"`
	Sequence    int64  `json:"sequence"`
	Kind        string `json:"kind"`
	Reason      string `json:"reason"`
	Quantity    string `json:"quantity"`
	OnHandAfter string `json:"on_hand_after"`
	Note        string `json:"note"`
	ActorID     string `json:"actor_id"`
	OccurredAt  string `json:"occurred_at"`
	ReversesID  string `json:"reverses_id,omitempty"`
}

func toMovementJSON(m inventory.Movement, scale int32) movementJSON {
	out := movementJSON{ID: m.ID.String(), Sequence: m.Sequence, Kind: string(m.Kind), Reason: string(m.Reason),
		Quantity: m.Quantity.StringFixed(scale), OnHandAfter: m.OnHandAfter.StringFixed(scale), Note: m.Note,
		ActorID: m.ActorID.String(), OccurredAt: rfc3339(m.OccurredAt)}
	if !m.ReversesID.IsZero() {
		out.ReversesID = m.ReversesID.String()
	}
	return out
}

var kindText = map[inventory.Kind]string{
	inventory.KindAdjustmentIn:  "entrada",
	inventory.KindAdjustmentOut: "salida",
	inventory.KindReversal:      "reverso",
}

// whereFlags agrega --company, --branch y --warehouse. Sin --company ni
// --branch se usan los de esta instalación.
func whereFlags(cmd *cobra.Command, w *stock.WarehouseRef) {
	cmd.Flags().StringVar(&w.Company, "company", "", "código de empresa (por defecto, la de esta instalación)")
	cmd.Flags().StringVar(&w.Branch, "branch", "", "código de sucursal (por defecto, la de esta instalación)")
	cmd.Flags().StringVar(&w.Warehouse, "warehouse", "", "código de almacén (por defecto, el único de la sucursal)")
}

func withDefaults(w stock.WarehouseRef, s appSession) stock.WarehouseRef {
	if w.Company == "" {
		w.Company = s.cur.Company.Code.String()
		if w.Branch == "" {
			w.Branch = s.cur.Branch.Code.String()
		}
	}
	return w
}

func newInventoryCmd(env Env, flags *globalFlags) *cobra.Command {
	cmd := &cobra.Command{Use: "inventory", Short: "Existencias y kardex por almacén", Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() }}

	for _, d := range []inventory.Direction{inventory.In, inventory.Out} {
		var in stock.AdjustInput
		use, short := "in", "Registra una entrada por ajuste (p. ej. carga inicial)"
		if d == inventory.Out {
			use, short = "out", "Registra una salida por ajuste (merma, consumo interno, corrección)"
		}
		c := &cobra.Command{
			Use:   use,
			Short: short,
			Long: short + ".\n\nMotivos: initial_load (solo entrada), correction, shrinkage (solo salida),\n" +
				"internal_use (solo salida) y other (exige --note). Una salida nunca deja el\n" +
				"disponible en negativo (código 10). Con --key, repetir el mismo comando no\n" +
				"duplica el movimiento. Un error se corrige con 'inventory reverse'.",
			Args: noArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				return withSession(cmd, env, flags, func(ctx context.Context, s appSession) error {
					in.Direction = d
					in.Where = withDefaults(in.Where, s)
					m, err := s.stock.Adjust(ctx, s.principal(), in)
					if err != nil {
						return appError(err)
					}
					return writeMovement(cmd, flags, m)
				})
			},
		}
		whereFlags(c, &in.Where)
		c.Flags().StringVar(&in.Product, "product", "", "SKU, código de barras o ID (obligatorio)")
		c.Flags().StringVar(&in.Quantity, "qty", "", "cantidad positiva, con los decimales de la unidad (obligatoria)")
		c.Flags().StringVar(&in.Reason, "reason", "", "motivo (obligatorio; vea 'inventory reasons')")
		c.Flags().StringVar(&in.Note, "note", "", "nota explicativa")
		c.Flags().StringVar(&in.IdempotencyKey, "key", "", "clave de idempotencia para reintentos seguros")
		markRequired(c, "product", "qty", "reason")
		cmd.AddCommand(c)
	}

	var revNote, revKey string
	reverse := &cobra.Command{
		Use:   "reverse MOVIMIENTO",
		Short: "Anula un movimiento con otro de signo contrario; el original queda en el kardex",
		Args:  exactArgs(1, "indique el ID del movimiento a revertir"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withSession(cmd, env, flags, func(ctx context.Context, s appSession) error {
				m, err := s.stock.Reverse(ctx, s.principal(), args[0], revNote, revKey)
				if err != nil {
					return appError(err)
				}
				return writeMovement(cmd, flags, m)
			})
		},
	}
	reverse.Flags().StringVar(&revNote, "note", "", "motivo del reverso")
	reverse.Flags().StringVar(&revKey, "key", "", "clave de idempotencia para reintentos seguros")
	cmd.AddCommand(reverse)

	var where stock.WarehouseRef
	var product string
	stockCmd := &cobra.Command{
		Use:   "stock",
		Short: "Muestra existencias: física, reservada, bloqueada y disponible",
		Long:  "Sin filtros muestra todos los almacenes que usted puede consultar.",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withSession(cmd, env, flags, func(ctx context.Context, s appSession) error {
				f := stock.StockFilter{Product: product}
				if where != (stock.WarehouseRef{}) {
					w := withDefaults(where, s)
					f.Where = &w
				}
				list, err := s.stock.Stock(ctx, s.principal(), f)
				if err != nil {
					return appError(err)
				}
				if flags.json {
					items := make([]balanceJSON, 0, len(list))
					for _, b := range list {
						items = append(items, toBalanceJSON(b))
					}
					return writeJSON(cmd.OutOrStdout(), listPayload("balanzapro.stock/v1", items))
				}
				tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', tabwriter.AlignRight)
				fmt.Fprintln(tw, "ALMACÉN\tSKU\tPRODUCTO\tFÍSICA\tRESERVADA\tBLOQUEADA\tDISPONIBLE\tUNIDAD\t")
				for _, b := range list {
					j := toBalanceJSON(b)
					fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t\n", b.Location, j.SKU, j.Name, j.OnHand, j.Reserved, j.Blocked, j.Available, j.Unit)
				}
				return tw.Flush()
			})
		},
	}
	whereFlags(stockCmd, &where)
	stockCmd.Flags().StringVar(&product, "product", "", "SKU, código de barras o ID")
	cmd.AddCommand(stockCmd)

	var kWhere stock.WarehouseRef
	var kProduct string
	var limit, offset int
	kardex := &cobra.Command{
		Use:   "kardex",
		Short: "Muestra los movimientos de un producto en un almacén, en orden",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withSession(cmd, env, flags, func(ctx context.Context, s appSession) error {
				b, list, err := s.stock.Kardex(ctx, s.principal(), withDefaults(kWhere, s), kProduct, limit, offset)
				if err != nil {
					return appError(err)
				}
				scale := b.Unit.Scale
				if flags.json {
					items := make([]movementJSON, 0, len(list))
					for _, m := range list {
						items = append(items, toMovementJSON(m, scale))
					}
					return writeJSON(cmd.OutOrStdout(), struct {
						Schema  string         `json:"schema"`
						Balance balanceJSON    `json:"balance"`
						Items   []movementJSON `json:"items"`
					}{"balanzapro.kardex/v1", toBalanceJSON(b), items})
				}
				out := cmd.OutOrStdout()
				fmt.Fprintf(out, "%s %s en %s — existencia %s %s\n", b.Product.SKU, b.Product.Name, b.Location, b.OnHand.StringFixed(scale), b.Unit.Code)
				tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
				fmt.Fprintln(tw, "#\tFECHA (VET)\tTIPO\tMOTIVO\tCANTIDAD\tSALDO\tNOTA\tID")
				for _, m := range list {
					fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", m.Sequence, vet(m.OccurredAt), kindText[m.Kind], m.Reason,
						m.Quantity.StringFixed(scale), m.OnHandAfter.StringFixed(scale), orDash(m.Note), m.ID)
				}
				return tw.Flush()
			})
		},
	}
	whereFlags(kardex, &kWhere)
	kardex.Flags().StringVar(&kProduct, "product", "", "SKU, código de barras o ID (obligatorio)")
	kardex.Flags().IntVar(&limit, "limit", 100, fmt.Sprintf("máximo de movimientos (1 a %d)", stock.MaxKardexLimit))
	kardex.Flags().IntVar(&offset, "offset", 0, "movimientos a saltar")
	markRequired(kardex, "product")
	cmd.AddCommand(kardex)

	cmd.AddCommand(&cobra.Command{
		Use:   "verify",
		Short: "Reproduce el kardex de cada saldo y confirma que coincide (código 3 si no)",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withSession(cmd, env, flags, func(ctx context.Context, s appSession) error {
				rep, err := s.stock.Verify(ctx, s.principal())
				if err != nil {
					return appError(err)
				}
				type problem struct {
					balanceJSON
					Detail string `json:"detail"`
				}
				problems := make([]problem, 0, len(rep.Problems))
				for _, p := range rep.Problems {
					problems = append(problems, problem{toBalanceJSON(p.BalanceView), p.Detail})
				}
				if flags.json {
					if err := writeJSON(cmd.OutOrStdout(), struct {
						Schema    string    `json:"schema"`
						Balances  int       `json:"balances"`
						Movements int       `json:"movements"`
						Problems  []problem `json:"problems"`
					}{"balanzapro.inventory-verify/v1", rep.Balances, rep.Movements, problems}); err != nil {
						return err
					}
				} else {
					out := cmd.OutOrStdout()
					for _, p := range problems {
						fmt.Fprintf(out, "[FAIL] %s/%s/%s %s: %s\n", p.Company, p.Branch, p.Warehouse, p.SKU, p.Detail)
					}
					fmt.Fprintf(out, "%d saldos y %d movimientos verificados, %d con diferencias\n", rep.Balances, rep.Movements, len(problems))
				}
				if len(problems) > 0 {
					return checkFailed()
				}
				return nil
			})
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "reasons",
		Short: "Muestra los motivos de ajuste",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			type item struct {
				Reason      string `json:"reason"`
				Description string `json:"description"`
				In          bool   `json:"in"`
				Out         bool   `json:"out"`
			}
			var items []item
			for _, r := range inventory.Reasons() {
				items = append(items, item{string(r.Reason), r.Description, r.In, r.Out})
			}
			if flags.json {
				return writeJSON(cmd.OutOrStdout(), listPayload("balanzapro.inventory-reasons/v1", items))
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "MOTIVO\tDESCRIPCIÓN\tENTRADA\tSALIDA")
			yes := map[bool]string{true: "sí", false: "no"}
			for _, i := range items {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", i.Reason, i.Description, yes[i.In], yes[i.Out])
			}
			return tw.Flush()
		},
	})
	return cmd
}

func writeMovement(cmd *cobra.Command, flags *globalFlags, m stock.MovementView) error {
	scale := m.Unit.Scale
	if flags.json {
		return writeJSON(cmd.OutOrStdout(), struct {
			Schema    string `json:"schema"`
			Company   string `json:"company_code"`
			Branch    string `json:"branch_code"`
			Warehouse string `json:"warehouse_code"`
			SKU       string `json:"sku"`
			Unit      string `json:"unit"`
			Replayed  bool   `json:"replayed"`
			movementJSON
		}{"balanzapro.movement/v1", m.Location.Company.Code.String(), m.Location.Branch.Code.String(), m.Location.Warehouse.Code.String(),
			m.Product.SKU.String(), m.Unit.Code, m.Replayed, toMovementJSON(m.Movement, scale)})
	}
	prefix := kindText[m.Kind] + " registrada"
	if m.Kind == inventory.KindReversal {
		prefix = "reverso registrado"
	}
	if m.Replayed {
		prefix = "ya registrado antes (misma --key); no se aplicó de nuevo"
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s: %s %s %s en %s; existencia %s %s (movimiento %s)\n", prefix, m.Quantity.StringFixed(scale), m.Unit.Code,
		m.Product.SKU, m.Location, m.OnHandAfter.StringFixed(scale), m.Unit.Code, m.ID)
	return nil
}
