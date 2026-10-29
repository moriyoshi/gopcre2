module gopcre2-nyanjit-probe

go 1.26.0

require (
	github.com/moriyoshi/nyanjit v0.0.0
	github.com/moriyoshi/nyanjit/backend v0.0.0
)

require (
	github.com/ebitengine/purego v0.12.0-alpha.1.0.20261009162626-eafd1cca3a52 // indirect
	golang.org/x/sys v0.31.0 // indirect
)

replace github.com/moriyoshi/nyanjit => ../../../../nyanjit

replace github.com/moriyoshi/nyanjit/backend => ../../../../nyanjit/backend
