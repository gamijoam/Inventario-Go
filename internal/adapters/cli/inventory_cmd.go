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
	// Valor y CPP exactos; null si la existencia no está valorada.
	Currency string  `json:"currency"`
	Value    *string `json:"value"`
	UnitCost *string `json:"unit_cost"`
}

func toBalanceJSON(b stock.BalanceView) balanceJSON {
	q := func(v interface{ StringFixed(int32) string }) string { return v.StringFixed(b.Unit.Scale) }
	return balanceJSON{b.Location.Company.Code.String(), b.Location.Branch.Code.String(), b.Location.Warehouse.Code.String(),
		b.ProductID.String(), b.Product.SKU.String(), b.Product.Name.String(), b.Unit.Code,
		q(b.OnHand), q(b.Reserved), q(b.Blocked), q(b.Available()), b.Version,
		b.Location.Company.Currency.Code(), exact(b.EffectiveValue()), exact(b.UnitCost())}
}

// exact es un importe en JSON: texto decimal exacto, o null si se desconoce.
func exact(a inventory.Amount) *string {
	if !a.Known {
		return nil
	}
	s := a.Value.String()
	return &s
}

// money muestra un importe para lectura humana, con 2 decimales; el valor
// exacto está en --json.
func money(a inventory.Amount) string {
	if !a.Known {
		return "sin valor"
	}
	return a.Value.StringFixed(2)
}

// unitCost muestra un CPP con 6 decimales.
func unitCost(a inventory.Amount) string {
	if !a.Known {
		return "—"
	}
	return a.Value.StringFixed(6)
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
	TransferID  string `json:"transfer_id,omitempty"`
	// Valoración exacta; null si el movimiento no tiene costo conocido.
	UnitCost        *string `json:"unit_cost"`
	Cost            *string `json:"cost"`
	ValueAdjustment string  `json:"value_adjustment"`
	ValueAfter      *string `json:"value_after"`
}

func toMovementJSON(m inventory.Movement, scale int32) movementJSON {
	out := movementJSON{ID: m.ID.String(), Sequence: m.Sequence, Kind: string(m.Kind), Reason: string(m.Reason),
		Quantity: m.Quantity.StringFixed(scale), OnHandAfter: m.OnHandAfter.StringFixed(scale), Note: m.Note,
		ActorID: m.ActorID.String(), OccurredAt: rfc3339(m.OccurredAt),
		UnitCost: exact(m.UnitCost), Cost: exact(m.Cost), ValueAdjustment: m.ValueAdjustment.String(), ValueAfter: exact(m.ValueAfter)}
	if !m.ReversesID.IsZero() {
		out.ReversesID = m.ReversesID.String()
	}
	if !m.TransferID.IsZero() {
		out.TransferID = m.TransferID.String()
	}
	return out
}

var kindText = map[inventory.Kind]string{
	inventory.KindAdjustmentIn:  "entrada",
	inventory.KindAdjustmentOut: "salida",
	inventory.KindReversal:      "reverso",
	inventory.KindTransferOut:   "transf. salida",
	inventory.KindTransferIn:    "transf. entrada",
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
				"duplica el movimiento. Un error se corrige con 'inventory reverse'.\n\n" +
				"Valoración: una entrada lleva --unit-cost en la moneda de la empresa; sin él\n" +
				"entra al costo promedio vigente, que debe existir. Una salida se valora\n" +
				"siempre al costo promedio del almacén.",
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
		if d == inventory.In {
			c.Flags().StringVar(&in.UnitCost, "unit-cost", "", "costo unitario en la moneda de valoración (por defecto, el costo promedio vigente)")
		}
		markRequired(c, "product", "qty", "reason")
		cmd.AddCommand(c)
	}

	var revNote, revKey string
	reverse := &cobra.Command{
		Use:   "reverse MOVIMIENTO",
		Short: "Anula un movimiento con otro de signo contrario; el original queda en el kardex",
		Long: "Anula un movimiento con otro de signo contrario; el original queda en el kardex.\n\n" +
			"El reverso deshace exactamente el valor del original, no el costo promedio\n" +
			"actual: corregir un costo mal digitado no deja el error mezclado en el CPP.\n" +
			"Las transferencias se anulan con otra transferencia en sentido contrario.",
		Args: exactArgs(1, "indique el ID del movimiento a revertir"),
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
		Short: "Muestra existencias, valor y costo promedio por almacén",
		Long: "Muestra existencia física, reservada, bloqueada y disponible, el valor del\n" +
			"inventario y su costo promedio (CPP) en la moneda de la empresa. Sin filtros\n" +
			"muestra todos los almacenes que usted puede consultar.",
		Args: noArgs,
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
				fmt.Fprintln(tw, "ALMACÉN\tSKU\tPRODUCTO\tFÍSICA\tRESERVADA\tBLOQUEADA\tDISPONIBLE\tUNIDAD\tCPP\tVALOR\tMONEDA\t")
				for _, b := range list {
					j := toBalanceJSON(b)
					fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t\n", b.Location, j.SKU, j.Name, j.OnHand, j.Reserved, j.Blocked, j.Available, j.Unit,
						unitCost(b.UnitCost()), money(b.EffectiveValue()), j.Currency)
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
				fmt.Fprintf(out, "%s %s en %s — existencia %s %s, valor %s %s, CPP %s\n", b.Product.SKU, b.Product.Name, b.Location,
					b.OnHand.StringFixed(scale), b.Unit.Code, money(b.EffectiveValue()), b.Location.Company.Currency, unitCost(b.UnitCost()))
				tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
				fmt.Fprintln(tw, "#\tFECHA (VET)\tTIPO\tMOTIVO\tCANTIDAD\tSALDO\tCOSTO\tVALOR\tNOTA\tID")
				for _, m := range list {
					fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", m.Sequence, vet(m.OccurredAt), kindText[m.Kind], m.Reason,
						m.Quantity.StringFixed(scale), m.OnHandAfter.StringFixed(scale), movementCost(m), money(m.ValueAfter), orDash(m.Note), m.ID)
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
		Short: "Reproduce el kardex y el valor de cada saldo y confirma que coinciden (código 3 si no)",
		Long: "Reproduce el kardex de cada saldo, cantidad y valor, y lo compara con el\n" +
			"saldo guardado; comprueba además que cada transferencia saca y recibe la\n" +
			"misma cantidad y el mismo valor. La existencia sin valorar (cargada antes\n" +
			"del costo promedio) se informa como aviso, no como diferencia.",
		Args: noArgs,
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
				type transferProblem struct {
					TransferID string `json:"transfer_id"`
					Detail     string `json:"detail"`
				}
				problems := make([]problem, 0, len(rep.Problems))
				for _, p := range rep.Problems {
					problems = append(problems, problem{toBalanceJSON(p.BalanceView), p.Detail})
				}
				tproblems := make([]transferProblem, 0, len(rep.TransferProblems))
				for _, p := range rep.TransferProblems {
					tproblems = append(tproblems, transferProblem{p.TransferID.String(), p.Detail})
				}
				unvalued := make([]balanceJSON, 0, len(rep.Unvalued))
				for _, b := range rep.Unvalued {
					unvalued = append(unvalued, toBalanceJSON(b))
				}
				if flags.json {
					if err := writeJSON(cmd.OutOrStdout(), struct {
						Schema           string            `json:"schema"`
						Balances         int               `json:"balances"`
						Movements        int               `json:"movements"`
						Transfers        int               `json:"transfers"`
						Problems         []problem         `json:"problems"`
						TransferProblems []transferProblem `json:"transfer_problems"`
						Unvalued         []balanceJSON     `json:"unvalued"`
					}{"balanzapro.inventory-verify/v1", rep.Balances, rep.Movements, rep.Transfers, problems, tproblems, unvalued}); err != nil {
						return err
					}
				} else {
					out := cmd.OutOrStdout()
					for _, p := range problems {
						fmt.Fprintf(out, "[FAIL] %s/%s/%s %s: %s\n", p.Company, p.Branch, p.Warehouse, p.SKU, p.Detail)
					}
					for _, p := range tproblems {
						fmt.Fprintf(out, "[FAIL] transferencia %s: %s\n", p.TransferID, p.Detail)
					}
					for _, b := range unvalued {
						fmt.Fprintf(out, "[AVISO] %s/%s/%s %s: %s %s sin valorar; registre una entrada con --unit-cost\n", b.Company, b.Branch, b.Warehouse, b.SKU, b.OnHand, b.Unit)
					}
					fmt.Fprintf(out, "%d saldos, %d movimientos y %d transferencias verificados, %d con diferencias, %d sin valorar\n",
						rep.Balances, rep.Movements, rep.Transfers, len(problems)+len(tproblems), len(unvalued))
				}
				if !rep.OK() {
					return checkFailed()
				}
				return nil
			})
		},
	})

	cmd.AddCommand(newTransferCmd(env, flags))

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
	cur := m.Location.Company.Currency
	fmt.Fprintf(cmd.OutOrStdout(), "%s: %s %s %s en %s, costo %s %s; existencia %s %s, valor %s %s (movimiento %s)\n", prefix, m.Quantity.StringFixed(scale), m.Unit.Code,
		m.Product.SKU, m.Location, movementCost(m.Movement), cur, m.OnHandAfter.StringFixed(scale), m.Unit.Code, money(m.ValueAfter), cur, m.ID)
	return nil
}

// movementCost muestra el cambio de valor de un movimiento: su costo más el
// ajuste de valor, si lo hubo.
func movementCost(m inventory.Movement) string {
	if !m.Cost.Known {
		return "sin valor"
	}
	if m.ValueAdjustment.IsZero() {
		return m.Cost.Value.StringFixed(2)
	}
	return fmt.Sprintf("%s (ajuste %s)", m.Cost.Value.StringFixed(2), m.ValueAdjustment.StringFixed(2))
}

func newTransferCmd(env Env, flags *globalFlags) *cobra.Command {
	var in stock.TransferInput
	cmd := &cobra.Command{
		Use:   "transfer",
		Short: "Transfiere existencia entre dos almacenes de la misma sucursal",
		Long: "Saca la cantidad del almacén de origen a su costo promedio y la incorpora al\n" +
			"destino con exactamente ese valor, en una sola operación: no se gana ni se\n" +
			"pierde valor y el destino recalcula su costo promedio. Requiere el permiso\n" +
			"inventory.transfer.create en ambos almacenes. Entre sucursales no aplica: ese\n" +
			"traslado tendrá despacho y recepción propios. Con --key, repetir el comando no\n" +
			"duplica la transferencia.\n\n" +
			"Ejemplo:\n  balanzapro inventory transfer --from PRINCIPAL --to DEPOSITO --product HARINA --qty 4",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withSession(cmd, env, flags, func(ctx context.Context, s appSession) error {
				w := withDefaults(stock.WarehouseRef{Company: in.Company, Branch: in.Branch}, s)
				in.Company, in.Branch = w.Company, w.Branch
				t, err := s.stock.Transfer(ctx, s.principal(), in)
				if err != nil {
					return appError(err)
				}
				return writeTransfer(cmd, flags, t)
			})
		},
	}
	f := cmd.Flags()
	f.StringVar(&in.Company, "company", "", "código de empresa (por defecto, la de esta instalación)")
	f.StringVar(&in.Branch, "branch", "", "código de sucursal (por defecto, la de esta instalación)")
	f.StringVar(&in.From, "from", "", "código del almacén de origen (obligatorio)")
	f.StringVar(&in.To, "to", "", "código del almacén de destino (obligatorio)")
	f.StringVar(&in.Product, "product", "", "SKU, código de barras o ID (obligatorio)")
	f.StringVar(&in.Quantity, "qty", "", "cantidad positiva, con los decimales de la unidad (obligatoria)")
	f.StringVar(&in.Note, "note", "", "nota explicativa")
	f.StringVar(&in.IdempotencyKey, "key", "", "clave de idempotencia para reintentos seguros")
	markRequired(cmd, "from", "to", "product", "qty")
	return cmd
}

func writeTransfer(cmd *cobra.Command, flags *globalFlags, t stock.TransferView) error {
	scale := t.Unit.Scale
	cur := t.Origin.Company.Currency
	if flags.json {
		return writeJSON(cmd.OutOrStdout(), struct {
			Schema        string       `json:"schema"`
			ID            string       `json:"id"`
			Company       string       `json:"company_code"`
			Branch        string       `json:"branch_code"`
			FromWarehouse string       `json:"from_warehouse_code"`
			ToWarehouse   string       `json:"to_warehouse_code"`
			SKU           string       `json:"sku"`
			Unit          string       `json:"unit"`
			Quantity      string       `json:"quantity"`
			Currency      string       `json:"currency"`
			Value         string       `json:"value"`
			Note          string       `json:"note"`
			ActorID       string       `json:"actor_id"`
			OccurredAt    string       `json:"occurred_at"`
			Replayed      bool         `json:"replayed"`
			Out           movementJSON `json:"out"`
			In            movementJSON `json:"in"`
		}{"balanzapro.transfer/v1", t.ID.String(), t.Origin.Company.Code.String(), t.Origin.Branch.Code.String(),
			t.Origin.Warehouse.Code.String(), t.Destination.Warehouse.Code.String(), t.Product.SKU.String(), t.Unit.Code,
			t.Quantity.StringFixed(scale), cur.Code(), t.Value.String(), t.Note, t.ActorID.String(), rfc3339(t.OccurredAt), t.Replayed,
			toMovementJSON(t.Out, scale), toMovementJSON(t.In, scale)})
	}
	prefix := "transferencia registrada"
	if t.Replayed {
		prefix = "ya registrada antes (misma --key); no se aplicó de nuevo"
	}
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "%s: %s %s %s de %s a %s por %s %s (transferencia %s)\n", prefix, t.Quantity.StringFixed(scale), t.Unit.Code, t.Product.SKU,
		t.Origin.Warehouse.Code, t.Destination.Warehouse.Code, t.Value.StringFixed(2), cur, t.ID)
	fmt.Fprintf(out, "  %s: existencia %s %s, valor %s %s\n", t.Origin, t.Out.OnHandAfter.StringFixed(scale), t.Unit.Code, money(t.Out.ValueAfter), cur)
	fmt.Fprintf(out, "  %s: existencia %s %s, valor %s %s\n", t.Destination, t.In.OnHandAfter.StringFixed(scale), t.Unit.Code, money(t.In.ValueAfter), cur)
	return nil
}
