package cli

import (
	"context"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/gamijoam/inventario-go/internal/application/purchasing"
	"github.com/gamijoam/inventario-go/internal/application/stock"
	"github.com/gamijoam/inventario-go/internal/domain/exchange"
	"github.com/gamijoam/inventario-go/internal/domain/id"
	"github.com/gamijoam/inventario-go/internal/domain/purchase"
	"github.com/gamijoam/inventario-go/internal/domain/timezone"
)

// Compras: proveedores, órdenes, recepciones, gastos y costos (BP-M3-001),
// en la moneda de cada documento (BP-M3-002).

type supplierJSON struct {
	ID     string `json:"id"`
	Code   string `json:"code"`
	Name   string `json:"name"`
	RIF    string `json:"rif"`
	Status string `json:"status"`
}

func toSupplierJSON(s purchase.Supplier) supplierJSON {
	return supplierJSON{s.ID.String(), s.Code.String(), s.Name.String(), s.RIF.String(), string(s.Status)}
}

type orderLineJSON struct {
	LineNo   int    `json:"line_no"`
	SKU      string `json:"sku"`
	Unit     string `json:"unit"`
	Quantity string `json:"quantity"`
	UnitCost string `json:"unit_cost"`
	Received string `json:"received"`
	Pending  string `json:"pending"`
}

type orderJSON struct {
	ID        string          `json:"id"`
	Number    string          `json:"number"`
	Company   string          `json:"company_code"`
	Branch    string          `json:"branch_code"`
	Supplier  string          `json:"supplier_code"`
	Currency  string          `json:"currency"`
	Status    string          `json:"status"`
	Note      string          `json:"note"`
	CreatedBy string          `json:"created_by"`
	CreatedAt string          `json:"created_at"`
	ClosedAt  *string         `json:"closed_at"`
	Lines     []orderLineJSON `json:"lines"`
}

func toOrderJSON(v purchasing.OrderView) orderJSON {
	j := orderJSON{ID: v.ID.String(), Number: v.Number, Company: v.Company.Code.String(), Branch: v.Branch.Code.String(),
		Supplier: v.Supplier.Code.String(), Currency: v.Currency.Code(), Status: string(v.Status), Note: v.Note, CreatedBy: v.CreatedBy.String(),
		CreatedAt: rfc3339(v.CreatedAt), ClosedAt: optionalTime(v.ClosedAt), Lines: []orderLineJSON{}}
	for _, l := range v.Lines {
		p := v.Products[l.ProductID]
		sc := p.Unit.Scale
		j.Lines = append(j.Lines, orderLineJSON{l.LineNo, p.Product.SKU.String(), p.Unit.Code, l.Quantity.StringFixed(sc), l.UnitCost.String(),
			l.Received.StringFixed(sc), l.Pending().StringFixed(sc)})
	}
	return j
}

var orderText = map[purchase.OrderStatus]string{purchase.OrderOpen: "abierta", purchase.OrderReceived: "recibida", purchase.OrderCancelled: "cancelada"}

func writeOrder(cmd *cobra.Command, flags *globalFlags, v purchasing.OrderView) error {
	j := toOrderJSON(v)
	if flags.json {
		return writeJSON(cmd.OutOrStdout(), struct {
			Schema string `json:"schema"`
			orderJSON
		}{"balanzapro.purchase-order/v1", j})
	}
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "orden %s de %s para %s/%s en %s: %s\n", j.Number, v.Supplier.Name, j.Company, j.Branch, j.Currency, orderText[v.Status])
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "#\tSKU\tPEDIDO\tRECIBIDO\tPENDIENTE\tCOSTO\tUNIDAD")
	for _, l := range j.Lines {
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\t%s\n", l.LineNo, l.SKU, l.Quantity, l.Received, l.Pending, l.UnitCost, l.Unit)
	}
	return tw.Flush()
}

type allocationJSON struct {
	LineNo      int     `json:"line_no"`
	Base        string  `json:"base"`
	Amount      string  `json:"amount"`
	Capitalized string  `json:"capitalized"`
	Variance    string  `json:"variance"`
	MovementID  *string `json:"movement_id"`
}

// rateJSON es la instantánea de una tasa aplicada.
type rateJSON struct {
	ID        string `json:"id"`
	Base      string `json:"base"`
	Quote     string `json:"quote"`
	Rate      string `json:"rate"`
	Source    string `json:"source_type"`
	Label     string `json:"source_label"`
	Reference string `json:"source_reference"`
	ValidOn   string `json:"valid_on"`
	Version   int    `json:"version"`
	Note      string `json:"note"`
	CreatedBy string `json:"created_by"`
	CreatedAt string `json:"created_at"`
}

func toRateJSON(x exchange.Rate) rateJSON {
	return rateJSON{x.ID.String(), x.Base.Code(), x.Quote.Code(), x.Value.String(), string(x.Source.Type), x.Source.Label, x.Reference,
		x.ValidOn.Format(timezone.DateLayout), x.Version, x.Note, x.CreatedBy.String(), rfc3339(x.CreatedAt)}
}

func optionalRate(rates map[id.ID]exchange.Rate, rid id.ID) *rateJSON {
	x, ok := rates[rid]
	if !ok {
		return nil
	}
	j := toRateJSON(x)
	return &j
}

type expenseJSON struct {
	ID          string           `json:"id"`
	Concept     string           `json:"concept"`
	Amount      string           `json:"amount"`
	Currency    string           `json:"currency"`
	Date        string           `json:"expense_date"`
	Rate        *rateJSON        `json:"rate"`
	Valued      string           `json:"valued_amount"`
	Criterion   string           `json:"criterion"`
	Late        bool             `json:"late"`
	CreatedAt   string           `json:"created_at"`
	Allocations []allocationJSON `json:"allocations"`
}

type receiptLineJSON struct {
	LineNo      int     `json:"line_no"`
	SKU         string  `json:"sku"`
	Unit        string  `json:"unit"`
	OrderLineNo *int    `json:"order_line_no"`
	Quantity    string  `json:"quantity"`
	UnitCost    string  `json:"unit_cost"`   // moneda del documento
	BaseCost    string  `json:"base_cost"`   // moneda del documento
	Valued      *string `json:"valued_cost"` // moneda de valoración, al confirmar
	Expenses    string  `json:"expenses"`    // moneda de valoración
	TotalCost   *string `json:"total_cost"`  // moneda de valoración, al confirmar
	MovementID  *string `json:"movement_id"`
}

type receiptJSON struct {
	ID        string            `json:"id"`
	Number    string            `json:"number"`
	Company   string            `json:"company_code"`
	Branch    string            `json:"branch_code"`
	Warehouse string            `json:"warehouse_code"`
	Currency  string            `json:"currency"`
	Valuation string            `json:"valuation_currency"`
	Date      string            `json:"document_date"`
	Rate      *rateJSON         `json:"rate"`
	Supplier  string            `json:"supplier_code"`
	Order     *string           `json:"order_number"`
	Status    string            `json:"status"`
	Document  string            `json:"supplier_document"`
	Note      string            `json:"note"`
	CreatedBy string            `json:"created_by"`
	CreatedAt string            `json:"created_at"`
	ClosedBy  *string           `json:"closed_by"`
	ClosedAt  *string           `json:"closed_at"`
	Lines     []receiptLineJSON `json:"lines"`
	Expenses  []expenseJSON     `json:"expenses"`
	Movements []movementJSON    `json:"movements"`
	Replayed  bool              `json:"replayed"`
}

func toReceiptJSON(v purchasing.ReceiptView) receiptJSON {
	j := receiptJSON{ID: v.ID.String(), Number: v.Number, Company: v.Location.Company.Code.String(), Branch: v.Location.Branch.Code.String(),
		Warehouse: v.Location.Warehouse.Code.String(), Currency: v.Currency.Code(), Valuation: v.Valuation.Code(),
		Date: v.DocumentDate.Format(timezone.DateLayout), Rate: optionalRate(v.Rates, v.RateID), Supplier: v.Supplier.Code.String(),
		Status: string(v.Status), Document: v.SupplierDocument, Note: v.Note, CreatedBy: v.CreatedBy.String(), CreatedAt: rfc3339(v.CreatedAt),
		ClosedBy: optionalID(v.ClosedBy), ClosedAt: optionalTime(v.ClosedAt), Lines: []receiptLineJSON{}, Expenses: []expenseJSON{},
		Movements: []movementJSON{}, Replayed: v.Replayed}
	if v.Order != "" {
		o := v.Order
		j.Order = &o
	}
	scales := map[string]int32{}
	for _, l := range v.Lines {
		p := v.Products[l.ProductID]
		sc := p.Unit.Scale
		scales[l.ProductID.String()] = sc
		lj := receiptLineJSON{LineNo: l.LineNo, SKU: p.Product.SKU.String(), Unit: p.Unit.Code, Quantity: l.Quantity.StringFixed(sc),
			UnitCost: l.UnitCost.String(), BaseCost: l.BaseCost().String(), Expenses: l.Expenses.String(), MovementID: optionalID(l.MovementID)}
		if v.Status == purchase.ReceiptConfirmed {
			valued, total := l.Valued.String(), l.TotalCost().String()
			lj.Valued, lj.TotalCost = &valued, &total
		}
		if l.OrderLineNo > 0 {
			n := l.OrderLineNo
			lj.OrderLineNo = &n
		}
		j.Lines = append(j.Lines, lj)
	}
	for _, e := range v.Expenses {
		ej := expenseJSON{ID: e.ID.String(), Concept: e.Concept, Amount: e.Amount.String(), Currency: e.Currency.Code(),
			Date: e.Date.Format(timezone.DateLayout), Rate: optionalRate(v.Rates, e.RateID), Valued: e.Valued.String(), Criterion: string(e.Criterion), Late: e.Late,
			CreatedAt: rfc3339(e.CreatedAt), Allocations: []allocationJSON{}}
		for _, a := range e.Allocations {
			ej.Allocations = append(ej.Allocations, allocationJSON{a.LineNo, a.Base.String(), a.Amount.String(), a.Capitalized.String(),
				a.Variance.String(), optionalID(a.MovementID)})
		}
		j.Expenses = append(j.Expenses, ej)
	}
	for _, m := range v.Movements {
		j.Movements = append(j.Movements, toMovementJSON(m, scales[m.ProductID.String()]))
	}
	return j
}

var receiptText = map[purchase.ReceiptStatus]string{purchase.ReceiptDraft: "borrador", purchase.ReceiptConfirmed: "confirmada", purchase.ReceiptCancelled: "cancelada"}

func writeReceipt(cmd *cobra.Command, flags *globalFlags, v purchasing.ReceiptView) error {
	j := toReceiptJSON(v)
	if flags.json {
		return writeJSON(cmd.OutOrStdout(), struct {
			Schema string `json:"schema"`
			receiptJSON
		}{"balanzapro.purchase-receipt/v2", j})
	}
	out := cmd.OutOrStdout()
	order := ""
	if j.Order != nil {
		order = ", orden " + *j.Order
	}
	status := receiptText[v.Status]
	if v.Replayed {
		status += " (ya lo estaba; no se aplicó de nuevo)"
	}
	fmt.Fprintf(out, "recepción %s de %s en %s%s: %s\n", j.Number, v.Supplier.Name, v.Location, order, status)
	fmt.Fprintf(out, "  documento del %s en %s", j.Date, j.Currency)
	if j.Rate != nil {
		fmt.Fprintf(out, "; tasa aplicada %s (%s, vigente desde %s, versión %d)", v.Rates[v.RateID], j.Rate.Label, j.Rate.ValidOn, j.Rate.Version)
	} else if j.Currency != j.Valuation && v.Status == purchase.ReceiptDraft {
		fmt.Fprintf(out, "; se convierte a %s al confirmar, con la tasa vigente a esa fecha", j.Valuation)
	}
	fmt.Fprintln(out)
	if len(j.Lines) > 0 {
		tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		if j.Currency == j.Valuation {
			fmt.Fprintf(tw, "#\tSKU\tCANTIDAD\tCOSTO UNIT.\tCOMPRA\tGASTOS\tTOTAL\tMONEDA\n")
		} else {
			fmt.Fprintf(tw, "#\tSKU\tCANTIDAD\tCOSTO UNIT. %s\tCOMPRA %s\tCOMPRA %s\tGASTOS %s\tTOTAL %s\n", j.Currency, j.Currency, j.Valuation, j.Valuation, j.Valuation)
		}
		for _, l := range j.Lines {
			if j.Currency == j.Valuation {
				total := l.BaseCost
				if l.TotalCost != nil {
					total = *l.TotalCost
				}
				fmt.Fprintf(tw, "%d\t%s\t%s %s\t%s\t%s\t%s\t%s\t%s\n", l.LineNo, l.SKU, l.Quantity, l.Unit, l.UnitCost, l.BaseCost, l.Expenses, total, j.Currency)
				continue
			}
			fmt.Fprintf(tw, "%d\t%s\t%s %s\t%s\t%s\t%s\t%s\t%s\n", l.LineNo, l.SKU, l.Quantity, l.Unit, l.UnitCost, l.BaseCost, orDashPtr(l.Valued), l.Expenses, orDashPtr(l.TotalCost))
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}
	for _, e := range v.Expenses {
		state := "se prorratea al confirmar"
		if len(e.Allocations) > 0 {
			capitalized, variance := e.Allocations[0].Capitalized, e.Allocations[0].Variance
			for _, a := range e.Allocations[1:] {
				capitalized, variance = capitalized.Add(a.Capitalized), variance.Add(a.Variance)
			}
			state = "capitalizado " + capitalized.String()
			if !variance.IsZero() {
				state += ", diferencia " + variance.String()
			}
		}
		late := ""
		if e.Late {
			late = " (tardío)"
		}
		amount := e.Amount.String() + " " + e.Currency.Code()
		if e.Currency != v.Valuation {
			amount += " = " + e.Valued.String() + " " + v.Valuation.Code() + " a " + v.Rates[e.RateID].String()
		}
		fmt.Fprintf(out, "  gasto %s%s del %s: %s por %s; %s\n", e.Concept, late, e.Date.Format(timezone.DateLayout), amount, e.Criterion, state)
	}
	if n := len(v.Movements); n > 0 {
		fmt.Fprintf(out, "%d movimientos registrados en el kardex\n", n)
	}
	return nil
}

func newPurchaseCmd(env Env, flags *globalFlags) *cobra.Command {
	cmd := &cobra.Command{Use: "purchase", Short: "Compras: proveedores, órdenes, recepciones, gastos y costos", Args: noArgs,
		Long: "La recepción confirmada es lo único que mueve existencia: cada línea entra al\n" +
			"almacén con su costo de compra más los gastos prorrateados y recalcula el costo\n" +
			"promedio. El precio de venta no cambia. Cada documento va en su moneda; al\n" +
			"confirmar se convierte a la de valoración con la tasa vigente a la fecha de la\n" +
			"factura (vea 'balanzapro rate').",
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() }}
	cmd.AddCommand(newSupplierCmd(env, flags), newPurchaseOrderCmd(env, flags), newReceiptCmd(env, flags), newCostCmd(env, flags))
	return cmd
}

func newSupplierCmd(env Env, flags *globalFlags) *cobra.Command {
	cmd := &cobra.Command{Use: "supplier", Short: "Proveedores de la organización", Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() }}
	var code, name, rif string
	create := &cobra.Command{
		Use:   "create",
		Short: "Registra un proveedor (exige gestionar compras en toda la organización)",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withSession(cmd, env, flags, func(ctx context.Context, s appSession) error {
				v, err := s.buy.CreateSupplier(ctx, s.principal(), code, name, rif)
				if err != nil {
					return appError(err)
				}
				if flags.json {
					return writeJSON(cmd.OutOrStdout(), struct {
						Schema string `json:"schema"`
						supplierJSON
					}{"balanzapro.supplier/v1", toSupplierJSON(v)})
				}
				fmt.Fprintf(cmd.OutOrStdout(), "proveedor creado: %s %s%s\n", v.Code, v.Name, rifSuffix(v.RIF))
				return nil
			})
		},
	}
	create.Flags().StringVar(&code, "code", "", "código único (obligatorio)")
	create.Flags().StringVar(&name, "name", "", "razón social o nombre (obligatorio)")
	create.Flags().StringVar(&rif, "rif", "", "RIF (opcional)")
	markRequired(create, "code", "name")
	cmd.AddCommand(create)
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "Lista los proveedores",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withSession(cmd, env, flags, func(ctx context.Context, s appSession) error {
				list, err := s.buy.Suppliers(ctx, s.principal())
				if err != nil {
					return appError(err)
				}
				items := make([]supplierJSON, 0, len(list))
				for _, v := range list {
					items = append(items, toSupplierJSON(v))
				}
				if flags.json {
					return writeJSON(cmd.OutOrStdout(), listPayload("balanzapro.suppliers/v1", items))
				}
				tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
				fmt.Fprintln(tw, "CÓDIGO\tNOMBRE\tRIF")
				for _, v := range items {
					fmt.Fprintf(tw, "%s\t%s\t%s\n", v.Code, v.Name, orDash(v.RIF))
				}
				return tw.Flush()
			})
		},
	})
	return cmd
}

// parseLines lee --line PRODUCTO:CANTIDAD:COSTO.
func parseLines(raw []string) ([]purchasing.LineInput, error) {
	out := make([]purchasing.LineInput, 0, len(raw))
	for _, r := range raw {
		parts := strings.Split(r, ":")
		if len(parts) != 3 {
			return nil, usageErrorf("--line %q: use PRODUCTO:CANTIDAD:COSTO_UNITARIO", r)
		}
		out = append(out, purchasing.LineInput{Product: parts[0], Quantity: parts[1], UnitCost: parts[2]})
	}
	return out, nil
}

func newPurchaseOrderCmd(env Env, flags *globalFlags) *cobra.Command {
	cmd := &cobra.Command{Use: "order", Short: "Órdenes de compra (no mueven existencia)", Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() }}
	var in purchasing.OrderInput
	var lines []string
	create := &cobra.Command{
		Use:   "create",
		Short: "Crea una orden de compra para una sucursal",
		Long:  "Ejemplo:\n  balanzapro purchase order create --supplier DIST --line HARINA:100:1.25 --line ARROZ:50:0.9",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ls, err := parseLines(lines)
			if err != nil {
				return err
			}
			return withSession(cmd, env, flags, func(ctx context.Context, s appSession) error {
				w := withDefaults(stock.WarehouseRef{Company: in.Company, Branch: in.Branch}, s)
				in.Company, in.Branch, in.Lines = w.Company, w.Branch, ls
				v, err := s.buy.CreateOrder(ctx, s.principal(), in)
				if err != nil {
					return appError(err)
				}
				return writeOrder(cmd, flags, v)
			})
		},
	}
	create.Flags().StringVar(&in.Company, "company", "", "código de empresa (por defecto, la de esta instalación)")
	create.Flags().StringVar(&in.Branch, "branch", "", "código de sucursal (por defecto, la de esta instalación)")
	create.Flags().StringVar(&in.Supplier, "supplier", "", "código del proveedor (obligatorio)")
	create.Flags().StringVar(&in.Currency, "currency", "", "moneda de los costos, USD o VES (por defecto, la de valoración)")
	create.Flags().StringArrayVar(&lines, "line", nil, "PRODUCTO:CANTIDAD:COSTO_UNITARIO (repetible, al menos una)")
	create.Flags().StringVar(&in.Note, "note", "", "nota")
	markRequired(create, "supplier", "line")
	cmd.AddCommand(create)

	for _, c := range []struct {
		use, short string
		run        func(ctx context.Context, s appSession, ref string) (purchasing.OrderView, error)
	}{
		{"show ORDEN", "Muestra una orden", func(ctx context.Context, s appSession, ref string) (purchasing.OrderView, error) {
			return s.buy.ShowOrder(ctx, s.principal(), ref)
		}},
		{"cancel ORDEN", "Cancela lo pendiente de una orden abierta", func(ctx context.Context, s appSession, ref string) (purchasing.OrderView, error) {
			return s.buy.CancelOrder(ctx, s.principal(), ref)
		}},
	} {
		cmd.AddCommand(&cobra.Command{
			Use:   c.use,
			Short: c.short,
			Args:  exactArgs(1, "indique el número o ID de la orden"),
			RunE: func(cmd *cobra.Command, args []string) error {
				return withSession(cmd, env, flags, func(ctx context.Context, s appSession) error {
					v, err := c.run(ctx, s, args[0])
					if err != nil {
						return appError(err)
					}
					return writeOrder(cmd, flags, v)
				})
			},
		})
	}

	var status string
	list := &cobra.Command{
		Use:   "list",
		Short: "Lista las órdenes",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withSession(cmd, env, flags, func(ctx context.Context, s appSession) error {
				list, err := s.buy.Orders(ctx, s.principal(), purchase.OrderStatus(status))
				if err != nil {
					return appError(err)
				}
				items := make([]orderJSON, 0, len(list))
				for _, v := range list {
					items = append(items, toOrderJSON(v))
				}
				if flags.json {
					return writeJSON(cmd.OutOrStdout(), listPayload("balanzapro.purchase-orders/v1", items))
				}
				tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
				fmt.Fprintln(tw, "NÚMERO\tPROVEEDOR\tSUCURSAL\tESTADO\tLÍNEAS")
				for i, v := range list {
					fmt.Fprintf(tw, "%s\t%s\t%s/%s\t%s\t%d\n", v.Number, items[i].Supplier, items[i].Company, items[i].Branch, orderText[v.Status], len(v.Lines))
				}
				return tw.Flush()
			})
		},
	}
	list.Flags().StringVar(&status, "status", "", "open, received o cancelled")
	cmd.AddCommand(list)
	return cmd
}

func newReceiptCmd(env Env, flags *globalFlags) *cobra.Command {
	cmd := &cobra.Command{Use: "receipt", Short: "Recepciones de mercancía: borrador, gastos y confirmación", Args: noArgs,
		Long: "Flujo: create (directa o desde --order, que precarga lo pendiente) → line /\n" +
			"remove-line / expense → confirm. Al confirmar, los gastos se prorratean (por\n" +
			"valor o cantidad; el residuo de redondeo va a la línea mayor) y cada línea entra\n" +
			"al almacén con su costo total. Un gasto agregado después de confirmar revaloriza\n" +
			"solo lo que sigue en el almacén; lo que ya salió queda como diferencia de costo.",
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() }}

	var in purchasing.ReceiptInput
	create := &cobra.Command{
		Use:   "create",
		Short: "Abre una recepción en borrador",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withSession(cmd, env, flags, func(ctx context.Context, s appSession) error {
				in.Where = withDefaults(in.Where, s)
				v, err := s.buy.CreateReceipt(ctx, s.principal(), in)
				if err != nil {
					return appError(err)
				}
				return writeReceipt(cmd, flags, v)
			})
		},
	}
	whereFlags(create, &in.Where)
	create.Flags().StringVar(&in.Supplier, "supplier", "", "código del proveedor (con --order, por defecto el de la orden)")
	create.Flags().StringVar(&in.Order, "order", "", "número o ID de la orden de compra (opcional)")
	create.Flags().StringVar(&in.Currency, "currency", "", "moneda de la factura, USD o VES (por defecto, la de la orden o la de valoración)")
	create.Flags().StringVar(&in.Date, "date", "", "fecha de la factura, AAAA-MM-DD (por defecto, hoy); decide la tasa")
	create.Flags().StringVar(&in.Document, "document", "", "factura o guía del proveedor")
	create.Flags().StringVar(&in.Note, "note", "", "nota")
	cmd.AddCommand(create)

	var line purchasing.LineInput
	lineCmd := &cobra.Command{
		Use:   "line RECEPCIÓN",
		Short: "Agrega o reemplaza lo recibido de un producto (en borrador)",
		Args:  exactArgs(1, "indique el número o ID de la recepción"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withSession(cmd, env, flags, func(ctx context.Context, s appSession) error {
				v, err := s.buy.SetReceiptLine(ctx, s.principal(), args[0], line)
				if err != nil {
					return appError(err)
				}
				return writeReceipt(cmd, flags, v)
			})
		},
	}
	lineCmd.Flags().StringVar(&line.Product, "product", "", "SKU, código de barras o ID (obligatorio)")
	lineCmd.Flags().StringVar(&line.Quantity, "qty", "", "cantidad recibida (obligatoria)")
	lineCmd.Flags().StringVar(&line.UnitCost, "unit-cost", "", "costo unitario real de compra, sin gastos ni impuestos (obligatorio)")
	markRequired(lineCmd, "product", "qty", "unit-cost")
	cmd.AddCommand(lineCmd)

	var removeProduct string
	remove := &cobra.Command{
		Use:   "remove-line RECEPCIÓN",
		Short: "Quita un producto de una recepción en borrador",
		Args:  exactArgs(1, "indique el número o ID de la recepción"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withSession(cmd, env, flags, func(ctx context.Context, s appSession) error {
				v, err := s.buy.RemoveReceiptLine(ctx, s.principal(), args[0], removeProduct)
				if err != nil {
					return appError(err)
				}
				return writeReceipt(cmd, flags, v)
			})
		},
	}
	remove.Flags().StringVar(&removeProduct, "product", "", "SKU, código de barras o ID (obligatorio)")
	markRequired(remove, "product")
	cmd.AddCommand(remove)

	var ein purchasing.ExpenseInput
	expense := &cobra.Command{
		Use:   "expense RECEPCIÓN",
		Short: "Registra un gasto atribuible (flete, seguro…) a prorratear",
		Long: "En borrador, el gasto se prorratea al confirmar. Sobre una recepción ya\n" +
			"confirmada es un gasto tardío: exige purchase.receipt.confirm, revaloriza lo\n" +
			"que sigue en el almacén y deja como diferencia lo que ya salió. Un gasto en\n" +
			"otra moneda se convierte al registrarlo con la tasa vigente a su fecha.",
		Args: exactArgs(1, "indique el número o ID de la recepción"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withSession(cmd, env, flags, func(ctx context.Context, s appSession) error {
				v, err := s.buy.AddExpense(ctx, s.principal(), args[0], ein)
				if err != nil {
					return appError(err)
				}
				return writeReceipt(cmd, flags, v)
			})
		},
	}
	expense.Flags().StringVar(&ein.Concept, "concept", "", "concepto, p. ej. Flete (obligatorio)")
	expense.Flags().StringVar(&ein.Amount, "amount", "", "importe (obligatorio)")
	expense.Flags().StringVar(&ein.Currency, "currency", "", "moneda del gasto, USD o VES (por defecto, la de la recepción)")
	expense.Flags().StringVar(&ein.Date, "date", "", "fecha del comprobante, AAAA-MM-DD (por defecto, la de la factura; hoy si es tardío)")
	expense.Flags().StringVar(&ein.Criterion, "by", "value", "criterio de prorrateo: value o quantity")
	markRequired(expense, "concept", "amount")
	cmd.AddCommand(expense)

	for _, c := range []struct {
		use, short string
		run        func(ctx context.Context, s appSession, ref string) (purchasing.ReceiptView, error)
	}{
		{"confirm RECEPCIÓN", "Confirma: la mercancía entra al almacén y recalcula el costo promedio", func(ctx context.Context, s appSession, ref string) (purchasing.ReceiptView, error) {
			return s.buy.ConfirmReceipt(ctx, s.principal(), ref)
		}},
		{"cancel RECEPCIÓN", "Descarta una recepción en borrador", func(ctx context.Context, s appSession, ref string) (purchasing.ReceiptView, error) {
			return s.buy.CancelReceipt(ctx, s.principal(), ref)
		}},
		{"show RECEPCIÓN", "Muestra una recepción con sus líneas y gastos", func(ctx context.Context, s appSession, ref string) (purchasing.ReceiptView, error) {
			return s.buy.ShowReceipt(ctx, s.principal(), ref)
		}},
	} {
		cmd.AddCommand(&cobra.Command{
			Use:   c.use,
			Short: c.short,
			Args:  exactArgs(1, "indique el número o ID de la recepción"),
			RunE: func(cmd *cobra.Command, args []string) error {
				return withSession(cmd, env, flags, func(ctx context.Context, s appSession) error {
					v, err := c.run(ctx, s, args[0])
					if err != nil {
						return appError(err)
					}
					return writeReceipt(cmd, flags, v)
				})
			},
		})
	}

	var status string
	list := &cobra.Command{
		Use:   "list",
		Short: "Lista las recepciones",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withSession(cmd, env, flags, func(ctx context.Context, s appSession) error {
				list, err := s.buy.Receipts(ctx, s.principal(), purchase.ReceiptStatus(status))
				if err != nil {
					return appError(err)
				}
				items := make([]receiptJSON, 0, len(list))
				for _, v := range list {
					items = append(items, toReceiptJSON(v))
				}
				if flags.json {
					return writeJSON(cmd.OutOrStdout(), listPayload("balanzapro.purchase-receipts/v2", items))
				}
				tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
				fmt.Fprintln(tw, "NÚMERO\tPROVEEDOR\tALMACÉN\tESTADO\tLÍNEAS\tFECHA\tMONEDA\tDOCUMENTO")
				for i, v := range list {
					fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%s\t%s\t%s\n", v.Number, items[i].Supplier, v.Location, receiptText[v.Status], len(v.Lines),
						items[i].Date, items[i].Currency, orDash(v.SupplierDocument))
				}
				return tw.Flush()
			})
		},
	}
	list.Flags().StringVar(&status, "status", "", "draft, confirmed o cancelled")
	cmd.AddCommand(list)
	return cmd
}

func writeCosts(cmd *cobra.Command, flags *globalFlags, v purchasing.CostView) error {
	cur := v.Company.Currency.Code()
	type replacementJSON struct {
		UnitCost string `json:"unit_cost"`
		Currency string `json:"currency"`
		Note     string `json:"note"`
		ActorID  string `json:"actor_id"`
		At       string `json:"at"`
	}
	type lastJSON struct {
		UnitCost     string `json:"unit_cost"` // moneda del documento
		Currency     string `json:"currency"`
		Valued       string `json:"valued_unit_cost"`        // moneda de valoración
		WithExpenses string `json:"unit_cost_with_expenses"` // moneda de valoración
		Receipt      string `json:"receipt_number"`
		ReceivedAt   string `json:"received_at"`
	}
	j := struct {
		Schema      string            `json:"schema"`
		Company     string            `json:"company_code"`
		Currency    string            `json:"currency"`
		SKU         string            `json:"sku"`
		Unit        string            `json:"unit"`
		Warehouses  []balanceJSON     `json:"warehouses"`
		Last        *lastJSON         `json:"last_purchase"`
		Replacement []replacementJSON `json:"replacement_costs"`
	}{Schema: "balanzapro.product-costs/v2", Company: v.Company.Code.String(), Currency: cur, SKU: v.Product.SKU.String(), Unit: v.Unit.Code,
		Warehouses: []balanceJSON{}, Replacement: []replacementJSON{}}
	for _, b := range v.Balances {
		j.Warehouses = append(j.Warehouses, toBalanceJSON(b))
	}
	if v.Last != nil {
		j.Last = &lastJSON{v.Last.UnitCost.String(), v.Last.Currency.Code(), v.Last.ValuedUnit.String(), v.Last.TotalUnit.String(), v.Last.Receipt, rfc3339(v.Last.ReceivedAt)}
	}
	for _, r := range v.Replacement {
		j.Replacement = append(j.Replacement, replacementJSON{r.UnitCost.String(), r.Currency.Code(), r.Note, r.ActorID.String(), rfc3339(r.OccurredAt)})
	}
	if flags.json {
		return writeJSON(cmd.OutOrStdout(), j)
	}
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "%s %s en la empresa %s (%s)\n", j.SKU, v.Product.Name, j.Company, cur)
	for _, b := range v.Balances {
		fmt.Fprintf(out, "  costo promedio en %s: %s (existencia %s %s)\n", b.Location, unitCost(b.UnitCost()), b.OnHand.StringFixed(v.Unit.Scale), v.Unit.Code)
	}
	if j.Last != nil {
		price := j.Last.UnitCost
		if j.Last.Currency != cur {
			price = fmt.Sprintf("%s %s = %s", j.Last.UnitCost, j.Last.Currency, j.Last.Valued)
		}
		fmt.Fprintf(out, "  último costo de compra: %s (%s con gastos), recepción %s del %s\n", price, j.Last.WithExpenses, j.Last.Receipt, vet(v.Last.ReceivedAt))
	} else {
		fmt.Fprintln(out, "  último costo de compra: —")
	}
	if len(v.Replacement) > 0 {
		r := v.Replacement[0]
		fmt.Fprintf(out, "  costo de reposición estimado: %s %s (%s)%s\n", r.UnitCost, r.Currency, vet(r.OccurredAt), noteSuffix(r.Note))
	} else {
		fmt.Fprintln(out, "  costo de reposición estimado: —")
	}
	return nil
}

func noteSuffix(n string) string {
	if n == "" {
		return ""
	}
	return " — " + n
}

func newCostCmd(env Env, flags *globalFlags) *cobra.Command {
	cmd := &cobra.Command{Use: "cost", Short: "Costos de un producto: promedio, último de compra y reposición", Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() }}
	var company, product string
	show := &cobra.Command{
		Use:   "show",
		Short: "Muestra los costos de un producto en una empresa, sin mezclarlos",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withSession(cmd, env, flags, func(ctx context.Context, s appSession) error {
				v, err := s.buy.Costs(ctx, s.principal(), companyOr(company, s), product)
				if err != nil {
					return appError(err)
				}
				return writeCosts(cmd, flags, v)
			})
		},
	}
	show.Flags().StringVar(&company, "company", "", "código de empresa (por defecto, la de esta instalación)")
	show.Flags().StringVar(&product, "product", "", "SKU, código de barras o ID (obligatorio)")
	markRequired(show, "product")
	cmd.AddCommand(show)

	var setCompany, setProduct, cost, setCurrency, note string
	set := &cobra.Command{
		Use:   "set-replacement",
		Short: "Registra el costo de reposición estimado (no cambia el costo promedio ni precios)",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withSession(cmd, env, flags, func(ctx context.Context, s appSession) error {
				v, err := s.buy.SetReplacementCost(ctx, s.principal(), companyOr(setCompany, s), setProduct, cost, setCurrency, note)
				if err != nil {
					return appError(err)
				}
				return writeCosts(cmd, flags, v)
			})
		},
	}
	set.Flags().StringVar(&setCompany, "company", "", "código de empresa (por defecto, la de esta instalación)")
	set.Flags().StringVar(&setProduct, "product", "", "SKU, código de barras o ID (obligatorio)")
	set.Flags().StringVar(&cost, "unit-cost", "", "costo unitario estimado (obligatorio)")
	set.Flags().StringVar(&setCurrency, "currency", "", "moneda de la estimación, USD o VES (por defecto, la de valoración)")
	set.Flags().StringVar(&note, "note", "", "origen de la estimación, p. ej. cotización")
	markRequired(set, "product", "unit-cost")
	cmd.AddCommand(set)
	return cmd
}

func orDashPtr(s *string) string {
	if s == nil {
		return "—"
	}
	return *s
}
