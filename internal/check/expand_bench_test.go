package check

import "testing"

func BenchmarkRecaseToTerm(b *testing.B) {
	for _, c := range []struct{ name, term, observed string }{
		{"literal", "OpenAPI", "openapi"},
		{"optional", "OAuth2?", "Oauth"},
		{"alternation", "Docker(file|ize)", "dockerfile"},
		{"bails", "[Pp]ython", "python"},
	} {
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = recaseToTerm(c.term, c.observed)
			}
		})
	}
}
