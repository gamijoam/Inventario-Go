package cli

import (
	"context"
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	apppricing "github.com/gamijoam/inventario-go/internal/application/pricing"
	"github.com/gamijoam/inventario-go/internal/domain/decimal"
	"github.com/gamijoam/inventario-go/internal/domain/pricing"
)

// Listas y precios de venta (BP-M3-003, DEC-007.P2).

func optDec(d *decimal.Decimal) *string {
	if d == nil {
		return nil
	}
	s := d.String()
	return &s
}

type priceListJSON struct {
	ID       string  `json:"id"`
	Company  string  `json:"company_code"`
	Code     string  `json:"code"`
	Name     string  `json:"name"`
	Currency string  `json:"currency"`
	Status   string  `json:"status"`
	Margin   *string `json:"target_margin"`
}

func toPriceListJSON(v apppricing.ListView) priceListJSON {
	return priceListJSON{v.ID.String(), v.Company.Code.String(), v.Code.String(), v.Name, v.Currency.Code(), string(v.Status), optDec(v.Margin)}
}

type revisionJSON struct {
	ID          string  `json:"id"`
	Version     int     `json:"version"`
	Price       string  `json:"price"`
	EffectiveAt string  `json:"effective_at"`
	Status      string  `json:"status"`
	CreatedBy   string  `json:"created_by"`
	CancelledAt *string `json:"cancelled_at"`
}

func toRevisionJSON(r pricing.Revision) revisionJSON {
	return revisionJSON{r.ID.String(), r.Version, r.Price.String(), rfc3339(r.EffectiveAt), string(r.Status), r.CreatedBy.String(), optionalTime(r.CancelledAt)}
}

type referenceJSON struct {
	Basis    string    `json:"basis"`
	Cost     string    `json:"cost"`
	Currency string    `json:"currency"`
	Rate     *rateJSON `json:"rate"`
	ListCost string    `json:"list_cost"`
}

type proposalJSON struct {
	ID          string         `json:"id"`
	Number      string         `json:"number"`
	List        string         `json:"list_code"`
	Currency    string         `json:"currency"`
	SKU         string         `json:"sku"`
	Kind        string         `json:"kind"`
	Status      string         `json:"status"`
	BaseVersion int            `json:"base_version"`
	Current     *string        `json:"current_price"`
	Price       string         `json:"price"`
	Margin      *string        `json:"target_margin"`
	Reference   *referenceJSON `json:"cost_reference"`
	Receipt     *string        `json:"receipt_id"`
	Note        string         `json:"note"`
	ProposedBy  string         `json:"proposed_by"`
	CreatedAt   string         `json:"created_at"`
	DecidedBy   *string        `json:"decided_by"`
	DecidedAt   *string        `json:"decided_at"`
	Decision    string         `json:"decision_note"`
	Revision    *revisionJSON  `json:"revision"`
}

func toProposalJSON(v apppricing.ProposalView) proposalJSON {
	j := proposalJSON{ID: v.ID.String(), Number: v.Number, List: v.List.Code.String(), Currency: v.List.Currency.Code(), SKU: v.Product.SKU.String(),
		Kind: string(v.Kind), Status: string(v.Status), BaseVersion: v.BaseVersion, Current: optDec(v.Current), Price: v.Price.String(),
		Margin: optDec(v.Margin), Receipt: optionalID(v.ReceiptID), Note: v.Note, ProposedBy: v.ProposedBy.String(), CreatedAt: rfc3339(v.CreatedAt),
		DecidedBy: optionalID(v.DecidedBy), DecidedAt: optionalTime(v.DecidedAt), Decision: v.DecisionNote}
	if ref := v.Reference; ref != nil {
		j.Reference = &referenceJSON{Basis: string(ref.Basis), Cost: ref.Cost.String(), Currency: ref.Currency.Code(), ListCost: ref.ListCost.String()}
		if v.Rate != nil {
			r := toRateJSON(*v.Rate)
			j.Reference.Rate = &r
		}
	}
	if v.Revision != nil {
		r := toRevisionJSON(*v.Revision)
		j.Revision = &r
	}
	return j
}

var proposalText = map[pricing.ProposalStatus]string{pricing.ProposalPending: "pendiente de aprobación", pricing.ProposalApproved: "aprobada",
	pricing.ProposalRejected: "rechazada", pricing.ProposalSuperseded: "sustituida"}

var basisText = map[pricing.CostBasis]string{pricing.BasisReplacement: "costo de reposición", pricing.BasisLastPurchase: "último costo de compra"}

// change describe "10 → 12 USD" o "— → 12 USD".
func change(current *decimal.Decimal, price decimal.Decimal, currency string) string {
	from := "—"
	if current != nil {
		from = current.String()
	}
	return fmt.Sprintf("%s → %s %s", from, price, currency)
}

func writeProposal(cmd *cobra.Command, flags *globalFlags, v apppricing.ProposalView) error {
	if flags.json {
		return writeJSON(cmd.OutOrStdout(), struct {
			Schema string `json:"schema"`
			proposalJSON
		}{"balanzapro.price-proposal/v1", toProposalJSON(v)})
	}
	out := cmd.OutOrStdout()
	cur := v.List.Currency.Code()
	fmt.Fprintf(out, "propuesta %s: %s en %s, %s; %s\n", v.Number, v.Product.SKU, v.List.Code, change(v.Current, v.Price, cur), proposalText[v.Status])
	if ref := v.Reference; ref != nil {
		line := fmt.Sprintf("  referencia: %s %s %s", basisText[ref.Basis], ref.Cost, ref.Currency)
		if v.Rate != nil {
			line += fmt.Sprintf(" = %s %s a %s", ref.ListCost, cur, v.Rate)
		}
		if v.Margin != nil {
			line += fmt.Sprintf("; margen objetivo %s %%", v.Margin)
		}
		if m, ok := pricing.MarginOf(v.Price, ref.ListCost); ok {
			line += fmt.Sprintf("; margen resultante %s %%", m)
		}
		fmt.Fprintln(out, line)
		if v.BelowCost(v.Price) {
			fmt.Fprintln(out, "  bajo el costo de referencia: aprobarla exige pricing.override.approve_below_cost")
		}
	}
	if v.Revision != nil {
		when := "rige desde ya"
		if v.Revision.Scheduled(v.DecidedAt) {
			when = "programada para el " + vet(v.Revision.EffectiveAt) + " (hora de Caracas)"
		}
		fmt.Fprintf(out, "  revisión %d publicada: %s; afecta a las ventas futuras, no a las ya hechas\n", v.Revision.Version, when)
	}
	if v.DecisionNote != "" {
		fmt.Fprintf(out, "  motivo: %s\n", v.DecisionNote)
	}
	return nil
}

func newPriceListCmd(env Env, flags *globalFlags) *cobra.Command {
	cmd := &cobra.Command{Use: "price-list", Short: "Listas de precios de la empresa y sus márgenes objetivo", Args: noArgs,
		Long: "Cada lista (General, Mayorista…) es de la empresa y va en una sola moneda. Con\n" +
			"margen objetivo, confirmar una recepción le deja propuestas de precio; sin\n" +
			"margen, solo cambia a mano. Crear listas y fijar márgenes exige\n" +
			"pricing.list.manage con ámbito de empresa.",
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() }}

	var in apppricing.ListInput
	create := &cobra.Command{
		Use:   "create",
		Short: "Crea una lista de precios",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withSession(cmd, env, flags, func(ctx context.Context, s appSession) error {
				in.Company = companyOr(in.Company, s)
				v, err := s.price.CreateList(ctx, s.principal(), in)
				if err != nil {
					return appError(err)
				}
				if flags.json {
					return writeJSON(cmd.OutOrStdout(), struct {
						Schema string `json:"schema"`
						priceListJSON
					}{"balanzapro.price-list/v1", toPriceListJSON(v)})
				}
				fmt.Fprintf(cmd.OutOrStdout(), "lista creada: %s %s en %s%s\n", v.Code, v.Name, v.Currency, marginSuffix(v.Margin))
				return nil
			})
		},
	}
	create.Flags().StringVar(&in.Company, "company", "", "código de empresa (por defecto, la de esta instalación)")
	create.Flags().StringVar(&in.Code, "code", "", "código único en la empresa (obligatorio)")
	create.Flags().StringVar(&in.Name, "name", "", "nombre (obligatorio)")
	create.Flags().StringVar(&in.Currency, "currency", "", "moneda de la lista, USD o VES (por defecto, la de valoración)")
	create.Flags().StringVar(&in.Margin, "margin", "", "margen objetivo sobre venta, en % (opcional)")
	markRequired(create, "code", "name")
	cmd.AddCommand(create)

	var listCompany string
	list := &cobra.Command{
		Use:   "list",
		Short: "Lista las listas de precios",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withSession(cmd, env, flags, func(ctx context.Context, s appSession) error {
				list, err := s.price.Lists(ctx, s.principal(), companyOr(listCompany, s))
				if err != nil {
					return appError(err)
				}
				items := make([]priceListJSON, 0, len(list))
				for _, v := range list {
					items = append(items, toPriceListJSON(v))
				}
				if flags.json {
					return writeJSON(cmd.OutOrStdout(), listPayload("balanzapro.price-lists/v1", items))
				}
				tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
				fmt.Fprintln(tw, "CÓDIGO\tNOMBRE\tMONEDA\tMARGEN OBJETIVO\tESTADO")
				for _, v := range items {
					fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", v.Code, v.Name, v.Currency, percentOrDash(v.Margin), v.Status)
				}
				return tw.Flush()
			})
		},
	}
	list.Flags().StringVar(&listCompany, "company", "", "código de empresa (por defecto, la de esta instalación)")
	cmd.AddCommand(list)

	var margins apppricing.MarginInput
	var clear bool
	margin := &cobra.Command{
		Use:   "margin",
		Short: "Fija el margen objetivo de una lista o la excepción de un producto",
		Long: "Con --product fija la excepción de ese producto; sin él, el margen de la lista.\n" +
			"--clear lo quita: la lista deja de sugerir, o el producto vuelve al margen de\n" +
			"la lista. Los cambios quedan en el historial.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if clear == (margins.Margin != "") {
				return usageErrorf("indique --margin o --clear")
			}
			return withSession(cmd, env, flags, func(ctx context.Context, s appSession) error {
				margins.Company = companyOr(margins.Company, s)
				v, err := s.price.SetMargin(ctx, s.principal(), margins)
				if err != nil {
					return appError(err)
				}
				if flags.json {
					return writeJSON(cmd.OutOrStdout(), struct {
						Schema string `json:"schema"`
						priceListJSON
					}{"balanzapro.price-list/v1", toPriceListJSON(v)})
				}
				what := "margen de la lista " + v.Code.String()
				if margins.Product != "" {
					what = "excepción de " + margins.Product + " en " + v.Code.String()
				}
				state := "quitado"
				if !clear {
					state = margins.Margin + " %"
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", what, state)
				return nil
			})
		},
	}
	margin.Flags().StringVar(&margins.Company, "company", "", "código de empresa (por defecto, la de esta instalación)")
	margin.Flags().StringVar(&margins.List, "list", "", "código de la lista (obligatorio)")
	margin.Flags().StringVar(&margins.Product, "product", "", "SKU, código de barras o ID (opcional)")
	margin.Flags().StringVar(&margins.Margin, "margin", "", "margen objetivo sobre venta, en %")
	margin.Flags().BoolVar(&clear, "clear", false, "quita el margen")
	markRequired(margin, "list")
	cmd.AddCommand(margin)
	return cmd
}

func marginSuffix(m *decimal.Decimal) string {
	if m == nil {
		return " (sin margen objetivo)"
	}
	return fmt.Sprintf(" (margen objetivo %s %%)", m)
}

func percentOrDash(s *string) string {
	if s == nil {
		return "—"
	}
	return *s + " %"
}

func newPriceCmd(env Env, flags *globalFlags) *cobra.Command {
	cmd := &cobra.Command{Use: "price", Short: "Precios de venta: vigentes, propuestas y aprobación", Args: noArgs,
		Long: "Confirmar una recepción no cambia precios: deja propuestas en las listas con\n" +
			"margen objetivo (costo ÷ (1 − margen), sobre el costo de reposición o el último\n" +
			"de compra). Un precio cambia solo al aprobar una propuesta (pricing.price.approve\n" +
			"con ámbito de empresa), ya o desde una hora de Caracas, y afecta a las ventas\n" +
			"futuras de la lista, no a las ya hechas.",
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() }}

	var showCompany, showList, showProduct string
	show := &cobra.Command{
		Use:   "show",
		Short: "Muestra precios vigentes, programados y propuestas pendientes",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withSession(cmd, env, flags, func(ctx context.Context, s appSession) error {
				list, err := s.price.Prices(ctx, s.principal(), companyOr(showCompany, s), showList, showProduct)
				if err != nil {
					return appError(err)
				}
				type priceJSON struct {
					List      string         `json:"list_code"`
					Currency  string         `json:"currency"`
					SKU       string         `json:"sku"`
					Margin    *string        `json:"target_margin"`
					Current   *revisionJSON  `json:"current"`
					Scheduled []revisionJSON `json:"scheduled"`
					Pending   *string        `json:"pending_proposal"`
				}
				items := make([]priceJSON, 0, len(list))
				for _, v := range list {
					j := priceJSON{List: v.List.Code.String(), Currency: v.List.Currency.Code(), SKU: v.Product.SKU.String(), Margin: optDec(v.Margin),
						Scheduled: []revisionJSON{}}
					if v.Current != nil {
						r := toRevisionJSON(*v.Current)
						j.Current = &r
					}
					for _, x := range v.Scheduled {
						j.Scheduled = append(j.Scheduled, toRevisionJSON(x))
					}
					if v.Pending != nil {
						n := v.Pending.Number
						j.Pending = &n
					}
					items = append(items, j)
				}
				if flags.json {
					return writeJSON(cmd.OutOrStdout(), listPayload("balanzapro.prices/v1", items))
				}
				tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
				fmt.Fprintln(tw, "LISTA\tSKU\tPRECIO\tMONEDA\tPROGRAMADO\tPROPUESTA\tMARGEN OBJETIVO")
				for i, v := range list {
					price, scheduled, pending := "—", "—", "—"
					if v.Current != nil {
						price = v.Current.Price.String()
					}
					if len(v.Scheduled) > 0 {
						scheduled = fmt.Sprintf("%s desde %s", v.Scheduled[0].Price, vet(v.Scheduled[0].EffectiveAt))
					}
					if v.Pending != nil {
						pending = fmt.Sprintf("%s (%s)", v.Pending.Price, v.Pending.Number)
					}
					fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", items[i].List, items[i].SKU, price, items[i].Currency, scheduled, pending, percentOrDash(items[i].Margin))
				}
				return tw.Flush()
			})
		},
	}
	show.Flags().StringVar(&showCompany, "company", "", "código de empresa (por defecto, la de esta instalación)")
	show.Flags().StringVar(&showList, "list", "", "código de la lista (por defecto, todas)")
	show.Flags().StringVar(&showProduct, "product", "", "SKU, código de barras o ID (por defecto, todos los que tienen precio)")
	cmd.AddCommand(show)

	var pin apppricing.ProposeInput
	propose := &cobra.Command{
		Use:   "propose",
		Short: "Propone un precio (no cambia el vigente hasta aprobarlo)",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withSession(cmd, env, flags, func(ctx context.Context, s appSession) error {
				pin.Company = companyOr(pin.Company, s)
				v, err := s.price.Propose(ctx, s.principal(), pin)
				if err != nil {
					return appError(err)
				}
				return writeProposal(cmd, flags, v)
			})
		},
	}
	propose.Flags().StringVar(&pin.Company, "company", "", "código de empresa (por defecto, la de esta instalación)")
	propose.Flags().StringVar(&pin.List, "list", "", "código de la lista (obligatorio)")
	propose.Flags().StringVar(&pin.Product, "product", "", "SKU, código de barras o ID (obligatorio)")
	propose.Flags().StringVar(&pin.Price, "price", "", "precio en la moneda de la lista (obligatorio)")
	propose.Flags().StringVar(&pin.Note, "note", "", "motivo")
	markRequired(propose, "list", "product", "price")
	cmd.AddCommand(propose)

	var ain apppricing.ApproveInput
	approve := &cobra.Command{
		Use:   "approve PROPUESTA",
		Short: "Aprueba una propuesta y publica el precio, ya o desde --at",
		Args:  exactArgs(1, "indique el número o ID de la propuesta"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withSession(cmd, env, flags, func(ctx context.Context, s appSession) error {
				v, err := s.price.Approve(ctx, s.principal(), args[0], ain)
				if err != nil {
					return appError(err)
				}
				return writeProposal(cmd, flags, v)
			})
		},
	}
	approve.Flags().StringVar(&ain.Price, "price", "", "precio aprobado si difiere del propuesto")
	approve.Flags().StringVar(&ain.At, "at", "", "vigencia en hora de Caracas: AAAA-MM-DD o \"AAAA-MM-DD HH:MM\" (por defecto, ya)")
	cmd.AddCommand(approve)

	var rejectNote string
	reject := &cobra.Command{
		Use:   "reject PROPUESTA",
		Short: "Rechaza una propuesta; el precio vigente no cambia",
		Args:  exactArgs(1, "indique el número o ID de la propuesta"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withSession(cmd, env, flags, func(ctx context.Context, s appSession) error {
				v, err := s.price.Reject(ctx, s.principal(), args[0], rejectNote)
				if err != nil {
					return appError(err)
				}
				return writeProposal(cmd, flags, v)
			})
		},
	}
	reject.Flags().StringVar(&rejectNote, "note", "", "motivo del rechazo")
	cmd.AddCommand(reject)

	cmd.AddCommand(&cobra.Command{
		Use:   "proposal PROPUESTA",
		Short: "Muestra una propuesta con su costo de referencia",
		Args:  exactArgs(1, "indique el número o ID de la propuesta"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withSession(cmd, env, flags, func(ctx context.Context, s appSession) error {
				v, err := s.price.ShowProposal(ctx, s.principal(), args[0])
				if err != nil {
					return appError(err)
				}
				return writeProposal(cmd, flags, v)
			})
		},
	})

	var propCompany, propList, propStatus string
	proposals := &cobra.Command{
		Use:   "proposals",
		Short: "Lista las propuestas de precio",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withSession(cmd, env, flags, func(ctx context.Context, s appSession) error {
				list, err := s.price.Proposals(ctx, s.principal(), companyOr(propCompany, s), propList, pricing.ProposalStatus(propStatus))
				if err != nil {
					return appError(err)
				}
				items := make([]proposalJSON, 0, len(list))
				for _, v := range list {
					items = append(items, toProposalJSON(v))
				}
				if flags.json {
					return writeJSON(cmd.OutOrStdout(), listPayload("balanzapro.price-proposals/v1", items))
				}
				tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
				fmt.Fprintln(tw, "NÚMERO\tLISTA\tSKU\tCAMBIO\tORIGEN\tESTADO")
				for i, v := range list {
					origin := "manual"
					if v.Kind == pricing.KindSuggested {
						origin = "recepción"
					}
					fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", v.Number, items[i].List, items[i].SKU, change(v.Current, v.Price, items[i].Currency), origin, proposalText[v.Status])
				}
				return tw.Flush()
			})
		},
	}
	proposals.Flags().StringVar(&propCompany, "company", "", "código de empresa (por defecto, la de esta instalación)")
	proposals.Flags().StringVar(&propList, "list", "", "código de la lista (por defecto, todas)")
	proposals.Flags().StringVar(&propStatus, "status", "", "pending, approved, rejected o superseded")
	cmd.AddCommand(proposals)

	cmd.AddCommand(&cobra.Command{
		Use:   "cancel REVISIÓN",
		Short: "Anula un precio programado que todavía no rige",
		Args:  exactArgs(1, "indique el ID de la revisión"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withSession(cmd, env, flags, func(ctx context.Context, s appSession) error {
				v, err := s.price.CancelRevision(ctx, s.principal(), args[0])
				if err != nil {
					return appError(err)
				}
				if flags.json {
					return writeJSON(cmd.OutOrStdout(), struct {
						Schema string `json:"schema"`
						revisionJSON
					}{"balanzapro.price-revision/v1", toRevisionJSON(v)})
				}
				fmt.Fprintf(cmd.OutOrStdout(), "revisión %d anulada: %s no regirá desde %s\n", v.Version, v.Price, vet(v.EffectiveAt))
				return nil
			})
		},
	})
	return cmd
}
