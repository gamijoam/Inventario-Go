package cli

import (
	"context"
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/gamijoam/inventario-go/internal/application/rates"
	dmoney "github.com/gamijoam/inventario-go/internal/domain/money"
	"github.com/gamijoam/inventario-go/internal/domain/timezone"
)

// Tasas de cambio versionadas (BP-M3-002).

var sourceText = map[dmoney.RateSourceType]string{dmoney.SourceOfficial: "oficial", dmoney.SourceManual: "manual", dmoney.SourceProvider: "proveedor"}

type rateViewJSON struct {
	rateJSON
	Company string `json:"company_code"`
}

func toRateViewJSON(v rates.RateView) rateViewJSON {
	return rateViewJSON{toRateJSON(v.Rate), v.Company.Code.String()}
}

func describeRate(v rates.RateView) string {
	ref := ""
	if v.Reference != "" {
		ref = ", " + v.Reference
	}
	return fmt.Sprintf("%s (%s %s%s), vigente desde %s, versión %d", v.Rate, sourceText[v.Source.Type], v.Source.Label, ref,
		v.ValidOn.Format(timezone.DateLayout), v.Version)
}

func newRateCmd(env Env, flags *globalFlags) *cobra.Command {
	cmd := &cobra.Command{Use: "rate", Short: "Tasas de cambio de la empresa, con su fuente e historial", Args: noArgs,
		Long: "Una tasa dice cuántas unidades de --quote vale 1 unidad de --base: con\n" +
			"--base USD --quote VES --rate 45.725, 1 USD = 45.725 VES. Rige desde --date\n" +
			"(fecha comercial de Caracas) hasta que otra la reemplace. No se edita: registrar\n" +
			"otra para la misma fecha es una corrección (versión nueva). Los documentos ya\n" +
			"confirmados conservan la tasa que aplicaron. No hay descarga automática: quien\n" +
			"registra la tasa declara su fuente.",
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() }}

	var in rates.SetInput
	set := &cobra.Command{
		Use:   "set",
		Short: "Registra una tasa (exige currency.rate.manage con ámbito de empresa)",
		Long:  "Ejemplo:\n  balanzapro rate set --base USD --quote VES --rate 45.725 --source official --label BCV",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withSession(cmd, env, flags, func(ctx context.Context, s appSession) error {
				in.Company = companyOr(in.Company, s)
				v, err := s.fx.Set(ctx, s.principal(), in)
				if err != nil {
					return appError(err)
				}
				if flags.json {
					return writeJSON(cmd.OutOrStdout(), struct {
						Schema string `json:"schema"`
						rateViewJSON
					}{"balanzapro.exchange-rate/v1", toRateViewJSON(v)})
				}
				fmt.Fprintf(cmd.OutOrStdout(), "tasa registrada en %s: %s\n", v.Company.Code, describeRate(v))
				return nil
			})
		},
	}
	set.Flags().StringVar(&in.Company, "company", "", "código de empresa (por defecto, la de esta instalación)")
	set.Flags().StringVar(&in.Base, "base", "", "moneda de la que se da 1 unidad, USD o VES (obligatoria)")
	set.Flags().StringVar(&in.Quote, "quote", "", "moneda en que se expresa la tasa (obligatoria)")
	set.Flags().StringVar(&in.Rate, "rate", "", "unidades de --quote por 1 de --base, hasta 12 decimales (obligatoria)")
	set.Flags().StringVar(&in.Source, "source", "", "official, manual o provider (obligatoria)")
	set.Flags().StringVar(&in.Label, "label", "", "nombre de la fuente, p. ej. BCV (obligatorio)")
	set.Flags().StringVar(&in.Reference, "reference", "", "referencia de la publicación consultada")
	set.Flags().StringVar(&in.Date, "date", "", "fecha desde la que rige, AAAA-MM-DD (por defecto, hoy)")
	set.Flags().StringVar(&in.Note, "note", "", "motivo, p. ej. corrección")
	markRequired(set, "base", "quote", "rate", "source", "label")
	cmd.AddCommand(set)

	var showCompany, base, quote, date string
	show := &cobra.Command{
		Use:   "show",
		Short: "Muestra la tasa que rige en una fecha",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withSession(cmd, env, flags, func(ctx context.Context, s appSession) error {
				v, err := s.fx.Effective(ctx, s.principal(), companyOr(showCompany, s), base, quote, date)
				if err != nil {
					return appError(err)
				}
				if flags.json {
					return writeJSON(cmd.OutOrStdout(), struct {
						Schema string `json:"schema"`
						rateViewJSON
					}{"balanzapro.exchange-rate/v1", toRateViewJSON(v)})
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s\n", describeRate(v))
				return nil
			})
		},
	}
	show.Flags().StringVar(&showCompany, "company", "", "código de empresa (por defecto, la de esta instalación)")
	show.Flags().StringVar(&base, "base", "USD", "una moneda del par")
	show.Flags().StringVar(&quote, "quote", "VES", "la otra moneda del par")
	show.Flags().StringVar(&date, "date", "", "fecha, AAAA-MM-DD (por defecto, hoy)")
	cmd.AddCommand(show)

	var listCompany, pair string
	list := &cobra.Command{
		Use:   "list",
		Short: "Lista el historial de tasas, de la más antigua a la más reciente",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withSession(cmd, env, flags, func(ctx context.Context, s appSession) error {
				list, err := s.fx.List(ctx, s.principal(), companyOr(listCompany, s), pair)
				if err != nil {
					return appError(err)
				}
				items := make([]rateViewJSON, 0, len(list))
				for _, v := range list {
					items = append(items, toRateViewJSON(v))
				}
				if flags.json {
					return writeJSON(cmd.OutOrStdout(), listPayload("balanzapro.exchange-rates/v1", items))
				}
				tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
				fmt.Fprintln(tw, "DESDE\tVERSIÓN\tTASA\tFUENTE\tREGISTRADA\tNOTA")
				for _, v := range list {
					fmt.Fprintf(tw, "%s\t%d\t%s\t%s %s\t%s\t%s\n", v.ValidOn.Format(timezone.DateLayout), v.Version, v.Rate, sourceText[v.Source.Type],
						v.Source.Label, vet(v.CreatedAt), orDash(v.Note))
				}
				return tw.Flush()
			})
		},
	}
	list.Flags().StringVar(&listCompany, "company", "", "código de empresa (por defecto, la de esta instalación)")
	list.Flags().StringVar(&pair, "pair", "", "par, p. ej. USD/VES (por defecto, todos)")
	cmd.AddCommand(list)
	return cmd
}
