package cli

import (
	"context"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/gamijoam/inventario-go/internal/application/stock"
	"github.com/gamijoam/inventario-go/internal/domain/inventory"
	"github.com/gamijoam/inventario-go/internal/domain/timezone"
)

// Política de existencia negativa, reservas, bloqueos y conteos (BP-M2-003).

// parseExpires lee una fecha y hora de Caracas ("2026-10-28 18:00") o un
// instante RFC 3339.
func parseExpires(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, nil
	}
	for _, layout := range []string{"2006-01-02 15:04", "2006-01-02T15:04"} {
		if t, err := time.ParseInLocation(layout, s, timezone.Caracas()); err == nil {
			return t, nil
		}
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	return time.Time{}, usageErrorf("--expires %q: use AAAA-MM-DD HH:MM (hora de Caracas)", s)
}

func optionalTime(t time.Time) *string {
	if t.IsZero() {
		return nil
	}
	s := rfc3339(t)
	return &s
}

func optionalID(v interface {
	IsZero() bool
	String() string
}) *string {
	if v.IsZero() {
		return nil
	}
	s := v.String()
	return &s
}

// ---------- policy ----------

type policyJSON struct {
	Schema          string              `json:"schema"`
	Company         string              `json:"company_code"`
	NegativeEnabled bool                `json:"negative_enabled"`
	Limit           *string             `json:"negative_limit"`
	ChangeID        *string             `json:"change_id"`
	ChangedAt       *string             `json:"changed_at"`
	Products        []productPolicyJSON `json:"products"`
}

type productPolicyJSON struct {
	SKU       string  `json:"sku"`
	Unit      string  `json:"unit"`
	Mode      string  `json:"mode"`
	Limit     *string `json:"negative_limit"`
	Allowed   bool    `json:"negative_allowed"`
	Effective *string `json:"effective_limit"`
	ChangedAt *string `json:"changed_at"`
}

func writePolicy(cmd *cobra.Command, flags *globalFlags, v stock.PolicyView) error {
	cp := v.Current.Company
	j := policyJSON{Schema: "balanzapro.inventory-policy/v1", Company: v.Company.Code.String(), NegativeEnabled: cp.NegativeEnabled,
		ChangeID: optionalID(v.Current.ID), ChangedAt: optionalTime(v.Current.OccurredAt), Products: []productPolicyJSON{}}
	if cp.NegativeEnabled {
		j.Limit = exact(inventory.Known(cp.Limit))
	}
	for _, p := range v.Products {
		pj := productPolicyJSON{SKU: p.Product.SKU.String(), Unit: p.Unit.Code, Mode: string(p.PolicyChange.Product.Mode), Allowed: p.Allowed,
			ChangedAt: optionalTime(p.OccurredAt)}
		if pj.Mode == "" {
			pj.Mode = string(inventory.NegativeInherit)
		}
		if p.PolicyChange.Product.Mode == inventory.NegativeAllow {
			pj.Limit = exact(inventory.Known(p.PolicyChange.Product.Limit))
		}
		if p.Allowed {
			pj.Effective = exact(inventory.Known(p.Limit))
		}
		j.Products = append(j.Products, pj)
	}
	if flags.json {
		return writeJSON(cmd.OutOrStdout(), j)
	}
	out := cmd.OutOrStdout()
	if cp.NegativeEnabled {
		fmt.Fprintf(out, "empresa %s: existencia negativa habilitada, déficit máximo %s por producto y almacén\n", j.Company, *j.Limit)
	} else {
		fmt.Fprintf(out, "empresa %s: existencia negativa deshabilitada\n", j.Company)
	}
	if len(j.Products) == 0 {
		return nil
	}
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SKU\tMODO\tLÍMITE PROPIO\tPERMITIDO")
	for _, p := range j.Products {
		effective := "no"
		if p.Allowed {
			effective = "hasta " + *p.Effective + " " + p.Unit
		}
		limit := "—"
		if p.Limit != nil {
			limit = *p.Limit
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", p.SKU, p.Mode, limit, effective)
	}
	return tw.Flush()
}

func newPolicyCmd(env Env, flags *globalFlags) *cobra.Command {
	cmd := &cobra.Command{Use: "policy", Short: "Política de existencia negativa de la empresa y sus productos", Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() }}
	var company, product string
	show := &cobra.Command{
		Use:   "show",
		Short: "Muestra la política vigente",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withSession(cmd, env, flags, func(ctx context.Context, s appSession) error {
				v, err := s.stock.Policy(ctx, s.principal(), companyOr(company, s), product)
				if err != nil {
					return appError(err)
				}
				return writePolicy(cmd, flags, v)
			})
		},
	}
	show.Flags().StringVar(&company, "company", "", "código de empresa (por defecto, la de esta instalación)")
	show.Flags().StringVar(&product, "product", "", "un producto (SKU, código de barras o ID)")
	cmd.AddCommand(show)

	var setCompany, setProduct, negative, mode, limit string
	set := &cobra.Command{
		Use:   "set",
		Short: "Cambia la política de la empresa (--negative) o de un producto (--product --mode)",
		Long: "La existencia negativa está deshabilitada por defecto (DEC-007.F2). Para la\n" +
			"empresa: --negative on --limit N habilita un déficit de hasta N unidades por\n" +
			"producto y almacén; --negative off la deshabilita. Para un producto:\n" +
			"--mode inherit (sigue a la empresa), deny (nunca) o allow --limit N (límite\n" +
			"propio, si la empresa la habilitó). Cada cambio queda registrado y no altera\n" +
			"movimientos ya confirmados. Una salida en negativo exige además el permiso\n" +
			"extraordinario inventory.negative_stock.override y 'inventory out --allow-negative'.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withSession(cmd, env, flags, func(ctx context.Context, s appSession) error {
				var v stock.PolicyView
				var err error
				switch {
				case setProduct != "" && negative == "" && mode != "":
					v, err = s.stock.SetProductPolicy(ctx, s.principal(), companyOr(setCompany, s), setProduct, mode, limit)
				case setProduct == "" && mode == "" && (negative == "on" || negative == "off"):
					v, err = s.stock.SetCompanyPolicy(ctx, s.principal(), companyOr(setCompany, s), negative == "on", limit)
				default:
					return usageErrorf("use --negative on|off [--limit N] para la empresa, o --product P --mode inherit|deny|allow [--limit N]")
				}
				if err != nil {
					return appError(err)
				}
				return writePolicy(cmd, flags, v)
			})
		},
	}
	set.Flags().StringVar(&setCompany, "company", "", "código de empresa (por defecto, la de esta instalación)")
	set.Flags().StringVar(&negative, "negative", "", "on u off: habilita o deshabilita la existencia negativa en la empresa")
	set.Flags().StringVar(&setProduct, "product", "", "producto al que se aplica --mode")
	set.Flags().StringVar(&mode, "mode", "", "inherit, deny o allow")
	set.Flags().StringVar(&limit, "limit", "", "déficit máximo por producto y almacén")
	cmd.AddCommand(set)
	return cmd
}

func companyOr(code string, s appSession) string {
	if code == "" {
		return s.cur.Company.Code.String()
	}
	return code
}

// ---------- reservation ----------

type reservationJSON struct {
	ID         string        `json:"id"`
	Company    string        `json:"company_code"`
	Branch     string        `json:"branch_code"`
	Warehouse  string        `json:"warehouse_code"`
	SKU        string        `json:"sku"`
	Unit       string        `json:"unit"`
	Quantity   string        `json:"quantity"`
	Consumed   string        `json:"consumed"`
	Remaining  string        `json:"remaining"`
	Status     string        `json:"status"`
	Reference  string        `json:"reference"`
	Note       string        `json:"note"`
	ExpiresAt  *string       `json:"expires_at"`
	CreatedBy  string        `json:"created_by"`
	CreatedAt  string        `json:"created_at"`
	ClosedBy   *string       `json:"closed_by"`
	ClosedAt   *string       `json:"closed_at"`
	CloseNote  string        `json:"close_note"`
	Replayed   bool          `json:"replayed"`
	Dispatched *movementJSON `json:"movement,omitempty"`
}

func toReservationJSON(v stock.ReservationView) reservationJSON {
	sc := v.Unit.Scale
	j := reservationJSON{ID: v.ID.String(), Company: v.Location.Company.Code.String(), Branch: v.Location.Branch.Code.String(),
		Warehouse: v.Location.Warehouse.Code.String(), SKU: v.Product.SKU.String(), Unit: v.Unit.Code,
		Quantity: v.Quantity.StringFixed(sc), Consumed: v.Consumed.StringFixed(sc), Remaining: v.Remaining().StringFixed(sc),
		Status: string(v.Status), Reference: v.Reference, Note: v.Note, ExpiresAt: optionalTime(v.ExpiresAt), CreatedBy: v.CreatedBy.String(),
		CreatedAt: rfc3339(v.CreatedAt), ClosedBy: optionalID(v.ClosedBy), ClosedAt: optionalTime(v.ClosedAt), CloseNote: v.CloseNote, Replayed: v.Replayed}
	if !v.Movement.ID.IsZero() {
		m := toMovementJSON(v.Movement, sc)
		j.Dispatched = &m
	}
	return j
}

var holdText = map[inventory.HoldStatus]string{
	inventory.HoldActive: "activa", inventory.HoldConsumed: "despachada", inventory.HoldReleased: "liberada",
	inventory.HoldExpired: "vencida", inventory.HoldWrittenOff: "dada de baja",
}

func writeReservation(cmd *cobra.Command, flags *globalFlags, v stock.ReservationView, verb string) error {
	j := toReservationJSON(v)
	if flags.json {
		return writeJSON(cmd.OutOrStdout(), struct {
			Schema string `json:"schema"`
			reservationJSON
		}{"balanzapro.reservation/v1", j})
	}
	if v.Replayed {
		verb = "ya registrada antes (misma --key); no se aplicó de nuevo"
	}
	qty := j.Quantity
	if j.Dispatched != nil {
		qty = strings.TrimPrefix(j.Dispatched.Quantity, "-")
	}
	fmt.Fprintf(cmd.OutOrStdout(), "reserva %s: %s %s %s en %s para %s; pendiente %s, estado %s (reserva %s)\n", verb, qty, j.Unit, j.SKU,
		v.Location, j.Reference, j.Remaining, holdText[v.Status], j.ID)
	return nil
}

func newReservationCmd(env Env, flags *globalFlags) *cobra.Command {
	cmd := &cobra.Command{Use: "reservation", Short: "Reservas de existencia para pedidos: crear, despachar, liberar y vencer", Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() }}

	var in stock.ReserveInput
	var expires string
	create := &cobra.Command{
		Use:   "create",
		Short: "Reserva unidades libres de un almacén para un documento",
		Long: "Compromete unidades disponibles (ni reservadas ni bloqueadas) para un\n" +
			"documento, sin sacarlas del almacén: la existencia y el costo no cambian.\n" +
			"Nunca reserva más que el disponible. --expires es opcional (hora de Caracas);\n" +
			"sin él, la reserva dura hasta despacharla o liberarla.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			t, err := parseExpires(expires)
			if err != nil {
				return err
			}
			return withSession(cmd, env, flags, func(ctx context.Context, s appSession) error {
				in.Where, in.ExpiresAt = withDefaults(in.Where, s), t
				v, err := s.stock.Reserve(ctx, s.principal(), in)
				if err != nil {
					return appError(err)
				}
				return writeReservation(cmd, flags, v, "registrada")
			})
		},
	}
	whereFlags(create, &in.Where)
	create.Flags().StringVar(&in.Product, "product", "", "SKU, código de barras o ID (obligatorio)")
	create.Flags().StringVar(&in.Quantity, "qty", "", "cantidad (obligatoria)")
	create.Flags().StringVar(&in.Reference, "reference", "", "documento que origina la reserva, p. ej. el pedido (obligatoria)")
	create.Flags().StringVar(&expires, "expires", "", "vencimiento, AAAA-MM-DD HH:MM en hora de Caracas (opcional)")
	create.Flags().StringVar(&in.Note, "note", "", "nota")
	create.Flags().StringVar(&in.IdempotencyKey, "key", "", "clave de idempotencia para reintentos seguros")
	markRequired(create, "product", "qty", "reference")
	cmd.AddCommand(create)

	var cQty, cNote, cKey string
	consume := &cobra.Command{
		Use:   "consume RESERVA",
		Short: "Despacha unidades reservadas: baja la existencia y lo reservado a la vez",
		Long:  "Sin --qty despacha todo lo pendiente. La salida se valora al costo promedio.",
		Args:  exactArgs(1, "indique el ID de la reserva"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withSession(cmd, env, flags, func(ctx context.Context, s appSession) error {
				v, err := s.stock.ConsumeReservation(ctx, s.principal(), args[0], cQty, cNote, cKey)
				if err != nil {
					return appError(err)
				}
				return writeReservation(cmd, flags, v, "despachada")
			})
		},
	}
	consume.Flags().StringVar(&cQty, "qty", "", "cantidad a despachar (por defecto, todo lo pendiente)")
	consume.Flags().StringVar(&cNote, "note", "", "nota")
	consume.Flags().StringVar(&cKey, "key", "", "clave de idempotencia para reintentos seguros")
	cmd.AddCommand(consume)

	var rNote string
	release := &cobra.Command{
		Use:   "release RESERVA",
		Short: "Libera lo pendiente de una reserva; vuelve al disponible",
		Args:  exactArgs(1, "indique el ID de la reserva"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withSession(cmd, env, flags, func(ctx context.Context, s appSession) error {
				v, err := s.stock.ReleaseReservation(ctx, s.principal(), args[0], rNote)
				if err != nil {
					return appError(err)
				}
				return writeReservation(cmd, flags, v, "liberada")
			})
		},
	}
	release.Flags().StringVar(&rNote, "note", "", "motivo")
	cmd.AddCommand(release)

	cmd.AddCommand(&cobra.Command{
		Use:   "expire",
		Short: "Libera las reservas vencidas (apto para tareas programadas)",
		Long: "Libera una sola vez cada reserva vencida de los almacenes donde usted gestiona\n" +
			"reservas. Repetirlo o ejecutarlo en paralelo no libera dos veces. Las vencidas\n" +
			"también se liberan solas antes de cualquier operación que use su saldo.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withSession(cmd, env, flags, func(ctx context.Context, s appSession) error {
				n, err := s.stock.ExpireReservations(ctx, s.principal())
				if err != nil {
					return appError(err)
				}
				if flags.json {
					return writeJSON(cmd.OutOrStdout(), struct {
						Schema  string `json:"schema"`
						Expired int    `json:"expired"`
					}{"balanzapro.reservation-expire/v1", n})
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%d reservas vencidas liberadas\n", n)
				return nil
			})
		},
	})

	var q stock.HoldQuery
	var where stock.WarehouseRef
	var status string
	list := &cobra.Command{
		Use:   "list",
		Short: "Lista las reservas",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withSession(cmd, env, flags, func(ctx context.Context, s appSession) error {
				if where != (stock.WarehouseRef{}) {
					w := withDefaults(where, s)
					q.Where = &w
				}
				q.Status = inventory.HoldStatus(status)
				list, err := s.stock.Reservations(ctx, s.principal(), q)
				if err != nil {
					return appError(err)
				}
				items := make([]reservationJSON, 0, len(list))
				for _, v := range list {
					items = append(items, toReservationJSON(v))
				}
				if flags.json {
					return writeJSON(cmd.OutOrStdout(), listPayload("balanzapro.reservations/v1", items))
				}
				tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
				fmt.Fprintln(tw, "ID\tALMACÉN\tSKU\tREFERENCIA\tCANTIDAD\tPENDIENTE\tESTADO\tVENCE (VET)")
				for i, v := range list {
					j := items[i]
					expiresAt := "—"
					if !v.ExpiresAt.IsZero() {
						expiresAt = vet(v.ExpiresAt)
					}
					fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", j.ID, v.Location, j.SKU, j.Reference, j.Quantity, j.Remaining, holdText[v.Status], expiresAt)
				}
				return tw.Flush()
			})
		},
	}
	whereFlags(list, &where)
	list.Flags().StringVar(&q.Product, "product", "", "SKU, código de barras o ID")
	list.Flags().StringVar(&status, "status", "", "active, consumed, released o expired")
	cmd.AddCommand(list)
	return cmd
}

// ---------- block ----------

type blockJSON struct {
	ID        string        `json:"id"`
	Company   string        `json:"company_code"`
	Branch    string        `json:"branch_code"`
	Warehouse string        `json:"warehouse_code"`
	SKU       string        `json:"sku"`
	Unit      string        `json:"unit"`
	Quantity  string        `json:"quantity"`
	Reason    string        `json:"reason"`
	Note      string        `json:"note"`
	Status    string        `json:"status"`
	CreatedBy string        `json:"created_by"`
	CreatedAt string        `json:"created_at"`
	ClosedBy  *string       `json:"closed_by"`
	ClosedAt  *string       `json:"closed_at"`
	CloseNote string        `json:"close_note"`
	Replayed  bool          `json:"replayed"`
	WriteOff  *movementJSON `json:"movement,omitempty"`
}

func toBlockJSON(v stock.BlockView) blockJSON {
	j := blockJSON{ID: v.ID.String(), Company: v.Location.Company.Code.String(), Branch: v.Location.Branch.Code.String(),
		Warehouse: v.Location.Warehouse.Code.String(), SKU: v.Product.SKU.String(), Unit: v.Unit.Code, Quantity: v.Quantity.StringFixed(v.Unit.Scale),
		Reason: string(v.Reason), Note: v.Note, Status: string(v.Status), CreatedBy: v.CreatedBy.String(), CreatedAt: rfc3339(v.CreatedAt),
		ClosedBy: optionalID(v.ClosedBy), ClosedAt: optionalTime(v.ClosedAt), CloseNote: v.CloseNote, Replayed: v.Replayed}
	if !v.Movement.ID.IsZero() {
		m := toMovementJSON(v.Movement, v.Unit.Scale)
		j.WriteOff = &m
	}
	return j
}

func writeBlock(cmd *cobra.Command, flags *globalFlags, v stock.BlockView, verb string) error {
	j := toBlockJSON(v)
	if flags.json {
		return writeJSON(cmd.OutOrStdout(), struct {
			Schema string `json:"schema"`
			blockJSON
		}{"balanzapro.block/v1", j})
	}
	if v.Replayed {
		verb = "ya registrado antes (misma --key); no se aplicó de nuevo"
	}
	fmt.Fprintf(cmd.OutOrStdout(), "bloqueo %s: %s %s %s en %s por %s (bloqueo %s)\n", verb, j.Quantity, j.Unit, j.SKU, v.Location, j.Reason, j.ID)
	if j.WriteOff != nil {
		fmt.Fprintf(cmd.OutOrStdout(), "  merma registrada: costo %s %s (movimiento %s)\n", movementCost(v.Movement), v.Location.Company.Currency, j.WriteOff.ID)
	}
	return nil
}

func newBlockCmd(env Env, flags *globalFlags) *cobra.Command {
	cmd := &cobra.Command{Use: "block", Short: "Mercancía no vendible: bloquear, liberar o dar de baja", Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() }}

	var in stock.BlockInput
	create := &cobra.Command{
		Use:   "create",
		Short: "Bloquea unidades libres (daño, cuarentena…): bajan del disponible, no de la existencia",
		Long: "Motivos: damage, quarantine, quality, administrative, investigation. Un\n" +
			"bloqueo no es una salida ni una merma: la existencia y el costo no cambian.\n" +
			"Para dar de baja la mercancía, use 'inventory block write-off'.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withSession(cmd, env, flags, func(ctx context.Context, s appSession) error {
				in.Where = withDefaults(in.Where, s)
				v, err := s.stock.Block(ctx, s.principal(), in)
				if err != nil {
					return appError(err)
				}
				return writeBlock(cmd, flags, v, "registrado")
			})
		},
	}
	whereFlags(create, &in.Where)
	create.Flags().StringVar(&in.Product, "product", "", "SKU, código de barras o ID (obligatorio)")
	create.Flags().StringVar(&in.Quantity, "qty", "", "cantidad (obligatoria)")
	create.Flags().StringVar(&in.Reason, "reason", "", "damage, quarantine, quality, administrative o investigation (obligatorio)")
	create.Flags().StringVar(&in.Note, "note", "", "nota")
	create.Flags().StringVar(&in.IdempotencyKey, "key", "", "clave de idempotencia para reintentos seguros")
	markRequired(create, "product", "qty", "reason")
	cmd.AddCommand(create)

	for _, writeOff := range []bool{false, true} {
		var note string
		use, short, verb := "release BLOQUEO", "Libera un bloqueo: las unidades vuelven al disponible", "liberado"
		if writeOff {
			use, short, verb = "write-off BLOQUEO", "Da de baja lo bloqueado como merma (salida valorada; exige inventory.stock.adjust)", "dado de baja"
		}
		c := &cobra.Command{
			Use:   use,
			Short: short,
			Args:  exactArgs(1, "indique el ID del bloqueo"),
			RunE: func(cmd *cobra.Command, args []string) error {
				return withSession(cmd, env, flags, func(ctx context.Context, s appSession) error {
					v, err := s.stock.CloseBlock(ctx, s.principal(), args[0], writeOff, note)
					if err != nil {
						return appError(err)
					}
					return writeBlock(cmd, flags, v, verb)
				})
			},
		}
		c.Flags().StringVar(&note, "note", "", "motivo")
		cmd.AddCommand(c)
	}

	var q stock.HoldQuery
	var where stock.WarehouseRef
	var status string
	list := &cobra.Command{
		Use:   "list",
		Short: "Lista los bloqueos",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withSession(cmd, env, flags, func(ctx context.Context, s appSession) error {
				if where != (stock.WarehouseRef{}) {
					w := withDefaults(where, s)
					q.Where = &w
				}
				q.Status = inventory.HoldStatus(status)
				list, err := s.stock.Blocks(ctx, s.principal(), q)
				if err != nil {
					return appError(err)
				}
				items := make([]blockJSON, 0, len(list))
				for _, v := range list {
					items = append(items, toBlockJSON(v))
				}
				if flags.json {
					return writeJSON(cmd.OutOrStdout(), listPayload("balanzapro.blocks/v1", items))
				}
				tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
				fmt.Fprintln(tw, "ID\tALMACÉN\tSKU\tCANTIDAD\tMOTIVO\tESTADO\tNOTA")
				for i, v := range list {
					j := items[i]
					fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", j.ID, v.Location, j.SKU, j.Quantity, j.Reason, holdText[v.Status], orDash(j.Note))
				}
				return tw.Flush()
			})
		},
	}
	whereFlags(list, &where)
	list.Flags().StringVar(&q.Product, "product", "", "SKU, código de barras o ID")
	list.Flags().StringVar(&status, "status", "", "active, released o written_off")
	cmd.AddCommand(list)
	return cmd
}

// ---------- count ----------

type countLineJSON struct {
	SKU             string  `json:"sku"`
	Unit            string  `json:"unit"`
	Counted         string  `json:"counted"`
	Expected        string  `json:"expected"`
	Difference      string  `json:"difference"`
	ExpectedVersion int64   `json:"expected_version"`
	UnitCost        *string `json:"unit_cost"`
	CapturedBy      string  `json:"captured_by"`
	CapturedAt      string  `json:"captured_at"`
}

type countJSON struct {
	ID           string          `json:"id"`
	Company      string          `json:"company_code"`
	Branch       string          `json:"branch_code"`
	Warehouse    string          `json:"warehouse_code"`
	Status       string          `json:"status"`
	Note         string          `json:"note"`
	CreatedBy    string          `json:"created_by"`
	CreatedAt    string          `json:"created_at"`
	SubmittedBy  *string         `json:"submitted_by"`
	SubmittedAt  *string         `json:"submitted_at"`
	DecidedBy    *string         `json:"decided_by"`
	DecidedAt    *string         `json:"decided_at"`
	DecisionNote string          `json:"decision_note"`
	Lines        []countLineJSON `json:"lines"`
	Movements    []movementJSON  `json:"movements"`
}

func toCountLineJSON(l stock.CountLineView) countLineJSON {
	sc := l.Unit.Scale
	return countLineJSON{SKU: l.Product.SKU.String(), Unit: l.Unit.Code, Counted: l.Counted.StringFixed(sc), Expected: l.Expected.StringFixed(sc),
		Difference: l.Difference().StringFixed(sc), ExpectedVersion: l.ExpectedVersion, UnitCost: exact(l.UnitCost),
		CapturedBy: l.CapturedBy.String(), CapturedAt: rfc3339(l.CapturedAt)}
}

func toCountJSON(v stock.CountView) countJSON {
	j := countJSON{ID: v.ID.String(), Company: v.Location.Company.Code.String(), Branch: v.Location.Branch.Code.String(),
		Warehouse: v.Location.Warehouse.Code.String(), Status: string(v.Status), Note: v.Note, CreatedBy: v.CreatedBy.String(),
		CreatedAt: rfc3339(v.CreatedAt), SubmittedBy: optionalID(v.SubmittedBy), SubmittedAt: optionalTime(v.SubmittedAt),
		DecidedBy: optionalID(v.DecidedBy), DecidedAt: optionalTime(v.DecidedAt), DecisionNote: v.DecisionNote,
		Lines: []countLineJSON{}, Movements: []movementJSON{}}
	scales := map[string]int32{}
	for _, l := range v.Lines {
		j.Lines = append(j.Lines, toCountLineJSON(l))
		scales[l.ProductID.String()] = l.Unit.Scale
	}
	for _, m := range v.Movements {
		j.Movements = append(j.Movements, toMovementJSON(m, scales[m.ProductID.String()]))
	}
	return j
}

var countText = map[inventory.CountStatus]string{
	inventory.CountDraft: "borrador", inventory.CountInReview: "en revisión", inventory.CountApplied: "aplicado", inventory.CountRejected: "rechazado",
}

func writeCount(cmd *cobra.Command, flags *globalFlags, v stock.CountView) error {
	j := toCountJSON(v)
	if flags.json {
		return writeJSON(cmd.OutOrStdout(), struct {
			Schema string `json:"schema"`
			countJSON
		}{"balanzapro.count/v1", j})
	}
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "conteo %s en %s: %s, %d productos\n", j.ID, v.Location, countText[v.Status], len(j.Lines))
	if len(j.Lines) > 0 {
		tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "SKU\tCONTADO\tREGISTRADO\tDIFERENCIA\tUNIDAD")
		for _, l := range j.Lines {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", l.SKU, l.Counted, l.Expected, l.Difference, l.Unit)
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}
	if v.Status == inventory.CountApplied {
		fmt.Fprintf(out, "%d ajustes registrados en el kardex\n", len(j.Movements))
	}
	return nil
}

func newCountCmd(env Env, flags *globalFlags) *cobra.Command {
	cmd := &cobra.Command{Use: "count", Short: "Conteos físicos: contar, enviar a revisión y aprobar el ajuste", Args: noArgs,
		Long: "Flujo: create → record (por producto, se puede repetir) → submit → approve o\n" +
			"reject. Aprueba un usuario distinto de quien contó, con inventory.count.approve;\n" +
			"al aprobar se registra un ajuste por producto con la diferencia entre lo contado\n" +
			"y lo registrado al contar, sobre la existencia actual: lo vendido después del\n" +
			"conteo no se descuenta dos veces.",
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() }}

	var where stock.WarehouseRef
	var note string
	create := &cobra.Command{
		Use:   "create",
		Short: "Abre un conteo en borrador para un almacén",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withSession(cmd, env, flags, func(ctx context.Context, s appSession) error {
				v, err := s.stock.CreateCount(ctx, s.principal(), withDefaults(where, s), note)
				if err != nil {
					return appError(err)
				}
				return writeCount(cmd, flags, v)
			})
		},
	}
	whereFlags(create, &where)
	create.Flags().StringVar(&note, "note", "", "nota")
	cmd.AddCommand(create)

	var product, qty, unitCost string
	record := &cobra.Command{
		Use:   "record CONTEO",
		Short: "Registra lo contado de un producto (repetirlo reemplaza la captura)",
		Long: "Guarda lo contado y la existencia registrada en ese momento. --unit-cost es\n" +
			"opcional: el costo de lo que sobre si el producto no tiene costo promedio ni\n" +
			"un costo anterior en el almacén.",
		Args: exactArgs(1, "indique el ID del conteo"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withSession(cmd, env, flags, func(ctx context.Context, s appSession) error {
				l, err := s.stock.RecordCount(ctx, s.principal(), args[0], product, qty, unitCost)
				if err != nil {
					return appError(err)
				}
				j := toCountLineJSON(l)
				if flags.json {
					return writeJSON(cmd.OutOrStdout(), struct {
						Schema string `json:"schema"`
						countLineJSON
					}{"balanzapro.count-line/v1", j})
				}
				fmt.Fprintf(cmd.OutOrStdout(), "contado %s %s %s; registrado %s, diferencia %s\n", j.Counted, j.Unit, j.SKU, j.Expected, j.Difference)
				return nil
			})
		},
	}
	record.Flags().StringVar(&product, "product", "", "SKU, código de barras o ID (obligatorio)")
	record.Flags().StringVar(&qty, "qty", "", "cantidad contada, cero o más (obligatoria)")
	record.Flags().StringVar(&unitCost, "unit-cost", "", "costo unitario de lo que sobre (opcional)")
	markRequired(record, "product", "qty")
	cmd.AddCommand(record)

	type step struct {
		use, short string
		run        func(ctx context.Context, s appSession, id, note string) (stock.CountView, error)
	}
	for _, st := range []step{
		{"submit CONTEO", "Envía el conteo a revisión; ya no admite capturas", func(ctx context.Context, s appSession, id, _ string) (stock.CountView, error) {
			return s.stock.SubmitCount(ctx, s.principal(), id)
		}},
		{"approve CONTEO", "Aprueba el conteo y registra los ajustes (otro usuario; inventory.count.approve)", func(ctx context.Context, s appSession, id, note string) (stock.CountView, error) {
			return s.stock.DecideCount(ctx, s.principal(), id, true, note)
		}},
		{"reject CONTEO", "Rechaza el conteo sin ajustar nada", func(ctx context.Context, s appSession, id, note string) (stock.CountView, error) {
			return s.stock.DecideCount(ctx, s.principal(), id, false, note)
		}},
		{"show CONTEO", "Muestra un conteo y sus líneas", func(ctx context.Context, s appSession, id, _ string) (stock.CountView, error) {
			return s.stock.ShowCount(ctx, s.principal(), id)
		}},
	} {
		var n string
		c := &cobra.Command{
			Use:   st.use,
			Short: st.short,
			Args:  exactArgs(1, "indique el ID del conteo"),
			RunE: func(cmd *cobra.Command, args []string) error {
				return withSession(cmd, env, flags, func(ctx context.Context, s appSession) error {
					v, err := st.run(ctx, s, args[0], n)
					if err != nil {
						return appError(err)
					}
					return writeCount(cmd, flags, v)
				})
			},
		}
		if strings.HasPrefix(st.use, "approve") || strings.HasPrefix(st.use, "reject") {
			c.Flags().StringVar(&n, "note", "", "nota de la decisión")
		}
		cmd.AddCommand(c)
	}

	var lWhere stock.WarehouseRef
	var status string
	list := &cobra.Command{
		Use:   "list",
		Short: "Lista los conteos",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withSession(cmd, env, flags, func(ctx context.Context, s appSession) error {
				var w *stock.WarehouseRef
				if lWhere != (stock.WarehouseRef{}) {
					ww := withDefaults(lWhere, s)
					w = &ww
				}
				list, err := s.stock.Counts(ctx, s.principal(), w, inventory.CountStatus(status))
				if err != nil {
					return appError(err)
				}
				items := make([]countJSON, 0, len(list))
				for _, v := range list {
					items = append(items, toCountJSON(v))
				}
				if flags.json {
					return writeJSON(cmd.OutOrStdout(), listPayload("balanzapro.counts/v1", items))
				}
				tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
				fmt.Fprintln(tw, "ID\tALMACÉN\tESTADO\tPRODUCTOS\tCREADO (VET)")
				for i, v := range list {
					fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\n", items[i].ID, v.Location, countText[v.Status], len(v.Lines), vet(v.CreatedAt))
				}
				return tw.Flush()
			})
		},
	}
	whereFlags(list, &lWhere)
	list.Flags().StringVar(&status, "status", "", "draft, in_review, applied o rejected")
	cmd.AddCommand(list)
	return cmd
}
