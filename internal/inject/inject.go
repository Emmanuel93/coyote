// Package inject marca texto de los repos con forma de orden para un agente:
// la inyección indirecta (LLM01:2025) y el envenenamiento de contexto (ASI06)
// llegan por lo que un agente lee junto a sus instrucciones (ADR-0023).
//
// Son reglas fijas, en español y en inglés, sobre el texto normalizado:
// minúsculas, sin acentos, sin caracteres invisibles y con los espacios
// juntos. Marcan; no borran. Un falso negativo es posible y por eso el gate
// sigue siendo la defensa que no depende de que el modelo obedezca.
package inject

import (
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// Finding es un texto marcado: la regla, su descripción y la línea (desde 1)
// del texto donde empieza.
type Finding struct {
	Rule  string `json:"rule"`
	Label string `json:"label"`
	Line  int    `json:"line"`
}

type rule struct {
	id, label string
	re        *regexp.Regexp
	// skip descarta una coincidencia mirando lo que sigue en el texto
	// normalizado (una llave pública no es una credencial).
	skip func(after string) bool
}

// Reglas sobre el texto normalizado. Buscan órdenes para un agente, no
// menciones de un tema: "ignora las instrucciones anteriores", no "ignora
// este archivo".
var rules = []rule{
	{id: "ignorar-instrucciones", label: "pide ignorar sus instrucciones", re: regexp.MustCompile(
		`\b(ignore|disregard|forget|override)\s+(all\s+|any\s+)?(of\s+)?(the\s+|your\s+|my\s+)?(previous|prior|above|earlier|preceding|system|original|initial)\s+(instructions?|prompts?|guidelines|directives|rules)\b` +
			`|\b(ignore|disregard|forget)\s+(all|any|every)\s+(of\s+)?(your\s+|the\s+)?(instructions?|prompts?|guidelines|directives)\b` +
			`|\b(ignore|disregard|forget)\s+your\s+(instructions?|prompts?|guidelines|directives|rules)\b` +
			`|\b(ignora|ignore|ignoren|olvida|olvide|olviden|omite|omita|descarta|descarte|desobedece|desobedezca)\s+(todas\s+|cualquier\s+)?(las\s+|tus\s+|sus\s+)?(instrucciones|indicaciones|ordenes|directrices|reglas)\s+(anteriores|previas|de arriba|del sistema|originales|iniciales|que (te|le|les) (dieron|dio|den|han dado)|que recibiste|que recibio)\b` +
			`|\b(ignora|ignore|ignoren|olvida|olvide|olviden|omite|omita|descarta|descarte)\s+todas\s+(las\s+|tus\s+|sus\s+)?(instrucciones|indicaciones|ordenes|directrices)\b` +
			`|\b(ignora|olvida|omite|descarta|desobedece)\s+tus\s+(instrucciones|indicaciones|ordenes|directrices|reglas)\b`)},
	{id: "cambio-de-rol", label: "pide cambiar de rol", re: regexp.MustCompile(
		`\byou are now (a|an|the|my|in)\s+(\w+\s+)?(assistant|agent|ai|model|bot|admin|administrator|root|developer mode|unrestricted|jailbroken|dan)\b` +
			`|\bfrom now on,?\s+you\s+(are|will|must|shall|should)\s+(ignore|obey|follow|only|always|never|act)\b` +
			`|\bnew (system )?(instructions|prompt)\s*:|\bsystem prompt\s*:\s*you\b` +
			`|\b(a partir de ahora|desde ahora),?\s+(eres|seras|actuaras|obedeceras|debes ignorar|solo obedeces)\b` +
			`|\bahora eres (un|una|el|la|mi)\s+(\w+\s+)?(asistente|agente|modelo|ia|bot|administrador|root)\b` +
			`|\bnuevas instrucciones\s*:`)},
	{id: "ocultar-a-la-persona", label: "pide ocultarle algo a la persona", re: regexp.MustCompile(
		`\b(do not|don't|dont|never)\s+(tell|inform|mention|reveal|show|notify|alert)\s+(this|these|that|it|anything)\s+(to\s+)?(the\s+)?(user|human|developer|reviewer|operator)\b` +
			`|\b(do not|don't|dont|never)\s+(tell|inform|notify|alert)\s+(the\s+)?(user|human|developer|reviewer|operator)\s+(about\s+)?(this|these|that you|what you)\b` +
			`|\bkeep (this|it) (secret|hidden) from (the\s+)?(user|human|developer|reviewer)\b` +
			`|\b(no|nunca)\s+(le\s+)?(digas|diga|menciones|mencione|muestres|cuentes|comentes|avises|avise|informes)\s+(esto|nada|eso|lo que hiciste|lo que haces)\s+(al|a la|a los|a las)\s+(usuario|usuaria|persona|humano|revisor|revisora|desarrollador|desarrolladora)\b` +
			`|\b(no|nunca)\s+(le\s+)?(avises|informes|digas)\s+(al|a la)\s+(usuario|usuaria|persona|humano|revisor|revisora)\s+(que|de esto|sobre esto)\b`)},
	{id: "descarga-y-ejecuta", label: "baja código de la red y lo ejecuta", re: regexp.MustCompile(
		`\b(curl|wget|iwr|irm|invoke-webrequest|invoke-restmethod)\b[^|\n]{0,300}\|\s*(sudo\s+)?(-\S+\s+)*(sh|bash|zsh|dash|ksh|fish|python3?|perl|ruby|node|iex|invoke-expression)\b` +
			`|\b(sh|bash|zsh|source)\s+(-c\s+)?["']?(<\(|\$\()\s*(curl|wget)\b` +
			`|\biex\s*\(?\s*(iwr|irm|\(?\s*new-object\s+net\.webclient)`)},
	{id: "lee-credenciales", label: "lee o manda credenciales", re: regexp.MustCompile(
		`\b(cat|less|more|head|tail|base64|xxd|cp|scp|type|nc)\s+(\S+\s+)*\S*(\.ssh/id_(rsa|dsa|ecdsa|ed25519)|\.aws/credentials|\.netrc|\.docker/config\.json)\b` +
			`|\bcurl\b[^|]{0,200}@\S*(\.ssh/id_(rsa|dsa|ecdsa|ed25519)|\.aws/credentials|\.netrc|\.env)\b` +
			`|\bgh\s+auth\s+token\b|\bsecurity\s+find-(generic|internet)-password\b` +
			`|\b(printenv|env)\s*\|\s*(curl|nc|ncat|base64|wget)\b|\bcat\s+(\S*/)?\.env\b`),
		skip: func(after string) bool {
			for _, s := range []string{".pub", ".example", ".sample", ".template", ".dist", ".tmpl"} {
				if strings.HasPrefix(after, s) {
					return true
				}
			}
			return false
		}},
	{id: "apaga-controles", label: "pide apagar el gate, los hooks o los permisos", re: regexp.MustCompile(
		`--no-verify\b|--dangerously-skip-permissions\b|\bbypasspermissions\b` +
			`|\bcoyote\s+approve\s+(--all|--bash)\b` +
			`|\b(disable|turn off|bypass|skip)\s+(the\s+)?(coyote\s+)?(gate|hooks?|guardrails?|permission checks?|sandbox)\b` +
			`|\b(desactiva|desactive|apaga|apague|salta|saltate|evita|esquiva)\s+(el\s+|los\s+|la\s+|las\s+)?(gate|hooks?|controles|permisos|sandbox)\b`)},
}

// commentRe encuentra comentarios HTML; agentWords, los que le hablan a un
// agente: un comentario no se ve en la vista del documento, pero el agente
// lo lee.
var (
	commentRe  = regexp.MustCompile(`(?s)<!--(.{0,1000}?)-->`)
	agentWords = regexp.MustCompile(`\b(ignore|ignora|instructions?|instrucciones|you are|eres un|eres una|assistant|asistente|ai agent|agente de ia|agente ia|llm|chatgpt|claude|copilot|gemini|system prompt)\b`)
)

// Scan devuelve lo marcado en text, una vez por regla y línea, en orden.
func Scan(text string) []Finding {
	var out []Finding
	seen := map[string]bool{}
	add := func(id, label string, line int) {
		k := id + "\x00" + strconv.Itoa(line)
		if !seen[k] {
			seen[k] = true
			out = append(out, Finding{Rule: id, Label: label, Line: line})
		}
	}
	norm, lines := normalize(text)
	for _, r := range rules {
		for _, m := range r.re.FindAllStringIndex(norm, -1) {
			if r.skip != nil && r.skip(norm[m[1]:]) {
				continue
			}
			add(r.id, r.label, lines[m[0]])
		}
	}
	for _, m := range commentRe.FindAllStringSubmatchIndex(norm, -1) {
		if agentWords.MatchString(norm[m[2]:m[3]]) {
			add("comentario-oculto", "comentario oculto que le habla a un agente", lines[m[0]])
		}
	}
	line := 1
	for _, r := range text {
		if r == '\n' {
			line++
			continue
		}
		if kind := hidden(r); kind != "" {
			add("texto-oculto", kind, line)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Line < out[j].Line })
	return out
}

// First devuelve la primera marca de text, si hay.
func First(text string) (Finding, bool) {
	f := Scan(text)
	if len(f) == 0 {
		return Finding{}, false
	}
	return f[0], true
}

// hidden describe un carácter que no se ve y puede esconder una orden.
func hidden(r rune) string {
	switch {
	case r >= 0xE0000 && r <= 0xE007F:
		return "etiquetas Unicode invisibles"
	case r >= 0x202A && r <= 0x202E, r >= 0x2066 && r <= 0x2069, r == 0x200E, r == 0x200F, r == 0x061C:
		return "controles de dirección del texto"
	case r == 0x200B, r == 0x2060, r == 0xFEFF, r == 0x180E, r >= 0x2061 && r <= 0x2064, r == 0x3164, r == 0x115F, r == 0x1160:
		return "caracteres de ancho cero"
	}
	return ""
}

// normalize deja el texto en minúsculas, sin acentos ni caracteres de
// formato, con los espacios juntos. lines da, por cada byte del resultado, la
// línea del texto original de donde salió.
func normalize(s string) (string, []int) {
	var b strings.Builder
	lines := make([]int, 0, len(s)+1)
	line, space := 1, false
	put := func(r rune) {
		n := b.Len()
		b.WriteRune(r)
		for i := n; i < b.Len(); i++ {
			lines = append(lines, line)
		}
	}
	for _, r := range s {
		if r == '\n' {
			if !space && b.Len() > 0 {
				put(' ')
				space = true
			}
			line++
			continue
		}
		if unicode.Is(unicode.Cf, r) || hidden(r) != "" {
			continue // una orden partida con caracteres invisibles se lee junta
		}
		if unicode.IsSpace(r) {
			if !space && b.Len() > 0 {
				put(' ')
				space = true
			}
			continue
		}
		space = false
		put(fold(unicode.ToLower(r)))
	}
	lines = append(lines, line) // el final del texto
	return b.String(), lines
}

// fold quita los acentos de las letras del español y del portugués.
func fold(r rune) rune {
	switch r {
	case 'á', 'à', 'â', 'ä', 'ã':
		return 'a'
	case 'é', 'è', 'ê', 'ë':
		return 'e'
	case 'í', 'ì', 'î', 'ï':
		return 'i'
	case 'ó', 'ò', 'ô', 'ö', 'õ':
		return 'o'
	case 'ú', 'ù', 'û', 'ü':
		return 'u'
	case 'ñ':
		return 'n'
	case 'ç':
		return 'c'
	case '’', '‘', 'ʼ':
		return '\''
	}
	return r
}

// instructionNames son los archivos que los IDEs y agentes leen como
// instrucciones, en minúsculas: en un disco que no distingue mayúsculas,
// agents.md es AGENTS.md.
var instructionNames = map[string]bool{
	"agents.md": true, "claude.md": true, "gemini.md": true, "context.coyote.md": true, "readme.coyote.md": true,
	".cursorrules": true, ".windsurfrules": true, ".clinerules": true, "copilot-instructions.md": true,
}

// InstructionFile informa si la ruta es un archivo de instrucciones de
// agentes, sin distinguir mayúsculas.
func InstructionFile(p string) bool {
	lp := strings.ToLower(strings.TrimPrefix(path.Clean("/"+p), "/"))
	if instructionNames[path.Base(lp)] {
		return true
	}
	return strings.HasPrefix(lp, ".github/instructions/") || strings.Contains(lp, "/.github/instructions/")
}

// docExts son los documentos para leer: ahí una orden para un agente no tiene
// excusa. El código queda fuera, con sus pruebas y sus cadenas.
var docExts = map[string]bool{".md": true, ".markdown": true, ".mdx": true, ".txt": true, ".rst": true, ".adoc": true, ".org": true}

// Document informa si la ruta es un documento de texto o un archivo de
// instrucciones de agentes.
func Document(p string) bool {
	if InstructionFile(p) {
		return true
	}
	return docExts[strings.ToLower(path.Ext(p))]
}
