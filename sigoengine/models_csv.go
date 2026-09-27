//**********************************************************************
//      sigoengine/models_csv.go
//**********************************************************************
//  Beschreibung: Modellliste als CSV exportieren (Registry-Format)
//**********************************************************************

package sigoengine

import (
	"encoding/csv"
	"io"
	"sort"
	"strconv"
)

// modelsCSVHeader: die ersten 11 Spalten entsprechen exakt dem
// Registry-Format von loadModelsFromCSV, danach Zusatzspalten. Die Kopfzeile
// beginnt mit '#', weil der Parser '#'-Zeilen als Kommentar überspringt —
// die Ausgabe ist damit direkt als models.csv für die CLI verwendbar.
var modelsCSVHeader = []string{
	"# id", "shortcode", "endpoint", "apikey", "max_input", "max_output",
	"input_cost", "output_cost", "min_temp", "max_temp", "requires_completion_tokens",
	"provider", "provider_code", "upstream_id",
}

// WriteModelsCSV schreibt die Modelle semikolon-getrennt nach w, sortiert nach
// Provider und Shortcode. Die Spalte apikey enthält nur den Namen der
// ENV-Variable, nie den Key selbst.
func WriteModelsCSV(w io.Writer, models []Model) error {
	type row struct {
		m        Model
		provider string
	}
	rows := make([]row, 0, len(models))
	for _, m := range models {
		rows = append(rows, row{m: m, provider: ResolveProvider(m.Endpoint, m.ID)})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].provider != rows[j].provider {
			return rows[i].provider < rows[j].provider
		}
		return rows[i].m.Shortcode < rows[j].m.Shortcode
	})

	cw := csv.NewWriter(w)
	cw.Comma = ';'
	if err := cw.Write(modelsCSVHeader); err != nil {
		return err
	}
	ff := func(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
	for _, r := range rows {
		m := r.m
		if err := cw.Write([]string{
			m.ID, m.Shortcode, m.Endpoint, m.APIKeyEnv,
			strconv.Itoa(m.MaxInputTokens), strconv.Itoa(m.MaxOutputTokens),
			ff(m.InputCost), ff(m.OutputCost), ff(m.MinTemperature), ff(m.MaxTemperature),
			strconv.FormatBool(m.RequiresCompletionTokens),
			r.provider, ProviderCode(r.provider), m.UpstreamID,
		}); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}
