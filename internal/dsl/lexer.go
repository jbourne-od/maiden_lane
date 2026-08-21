package dsl

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Lexer tokenizes DSL source text.
type Lexer struct {
	input   string
	pos     int  // current byte offset
	readPos int  // reading position (pos + 1 rune)
	ch      rune // current rune
	line    int  // 1-based line
	col     int  // 1-based column
}

// NewLexer creates a new Lexer for the given input.
func NewLexer(input string) *Lexer {
	l := &Lexer{
		input: input,
		line:  1,
		col:   0,
	}
	l.readChar()
	return l
}

func (l *Lexer) currentPos() Pos {
	return Pos{
		Line:   l.line,
		Column: l.col,
		Offset: l.pos,
	}
}

func (l *Lexer) readChar() {
	if l.readPos >= len(l.input) {
		l.ch = 0
		l.pos = l.readPos
		l.col++
	} else {
		r, width := utf8.DecodeRuneInString(l.input[l.readPos:])
		l.ch = r
		l.pos = l.readPos
		l.readPos += width
		l.col++
	}
}

func (l *Lexer) peekChar() rune {
	if l.readPos >= len(l.input) {
		return 0
	}
	r, _ := utf8.DecodeRuneInString(l.input[l.readPos:])
	return r
}

func (l *Lexer) skipWhitespaceAndComments() {
	for {
		if l.ch == '\n' {
			l.line++
			l.col = 0
			l.readChar()
			continue
		}
		if unicode.IsSpace(l.ch) {
			l.readChar()
			continue
		}
		// Single-line comment: # or //
		if l.ch == '#' || (l.ch == '/' && l.peekChar() == '/') {
			for l.ch != '\n' && l.ch != 0 {
				l.readChar()
			}
			continue
		}
		// Multi-line comment: /* ... */
		if l.ch == '/' && l.peekChar() == '*' {
			l.readChar() // consume /
			l.readChar() // consume *
			for {
				if l.ch == 0 {
					break
				}
				if l.ch == '\n' {
					l.line++
					l.col = 0
				}
				if l.ch == '*' && l.peekChar() == '/' {
					l.readChar() // consume *
					l.readChar() // consume /
					break
				}
				l.readChar()
			}
			continue
		}
		break
	}
}

// NextToken scans and returns the next token.
func (l *Lexer) NextToken() Token {
	l.skipWhitespaceAndComments()

	pos := l.currentPos()
	if l.ch == 0 {
		return Token{Type: TokenEOF, Literal: "", Pos: pos}
	}

	switch l.ch {
	case '.':
		l.readChar()
		return Token{Type: TokenDot, Literal: ".", Pos: pos}
	case ',':
		l.readChar()
		return Token{Type: TokenComma, Literal: ",", Pos: pos}
	case ':':
		// Check for atom shorthand `:ACTIVE`
		if isLetter(l.peekChar()) {
			l.readChar() // consume ':'
			atomLit := l.readIdentifier()
			return Token{Type: TokenAtom, Literal: atomLit, Pos: pos}
		}
		l.readChar()
		return Token{Type: TokenColon, Literal: ":", Pos: pos}
	case ';':
		l.readChar()
		return Token{Type: TokenSemicolon, Literal: ";", Pos: pos}
	case '(':
		l.readChar()
		return Token{Type: TokenLParen, Literal: "(", Pos: pos}
	case ')':
		l.readChar()
		return Token{Type: TokenRParen, Literal: ")", Pos: pos}
	case '{':
		l.readChar()
		return Token{Type: TokenLBrace, Literal: "{", Pos: pos}
	case '}':
		l.readChar()
		return Token{Type: TokenRBrace, Literal: "}", Pos: pos}
	case '[':
		l.readChar()
		return Token{Type: TokenLBracket, Literal: "[", Pos: pos}
	case ']':
		l.readChar()
		return Token{Type: TokenRBracket, Literal: "]", Pos: pos}
	case '+':
		l.readChar()
		return Token{Type: TokenPlus, Literal: "+", Pos: pos}
	case '-':
		if l.peekChar() == '>' {
			l.readChar()
			l.readChar()
			return Token{Type: TokenArrow, Literal: "->", Pos: pos}
		}
		l.readChar()
		return Token{Type: TokenMinus, Literal: "-", Pos: pos}
	case '*':
		l.readChar()
		return Token{Type: TokenStar, Literal: "*", Pos: pos}
	case '/':
		l.readChar()
		return Token{Type: TokenSlash, Literal: "/", Pos: pos}
	case '%':
		l.readChar()
		return Token{Type: TokenPercent, Literal: "%", Pos: pos}
	case '=':
		if l.peekChar() == '=' {
			l.readChar()
			l.readChar()
			return Token{Type: TokenEqual, Literal: "==", Pos: pos}
		}
		l.readChar()
		return Token{Type: TokenAssign, Literal: "=", Pos: pos}
	case '!':
		if l.peekChar() == '=' {
			l.readChar()
			l.readChar()
			return Token{Type: TokenNotEqual, Literal: "!=", Pos: pos}
		}
		l.readChar()
		return Token{Type: TokenNot, Literal: "!", Pos: pos}
	case '<':
		if l.peekChar() == '=' {
			l.readChar()
			l.readChar()
			return Token{Type: TokenLessEq, Literal: "<=", Pos: pos}
		}
		l.readChar()
		return Token{Type: TokenLess, Literal: "<", Pos: pos}
	case '>':
		if l.peekChar() == '=' {
			l.readChar()
			l.readChar()
			return Token{Type: TokenGreatEq, Literal: ">=", Pos: pos}
		}
		l.readChar()
		return Token{Type: TokenGreater, Literal: ">", Pos: pos}
	case '&':
		if l.peekChar() == '&' {
			l.readChar()
			l.readChar()
			return Token{Type: TokenAnd, Literal: "&&", Pos: pos}
		}
		l.readChar()
		return Token{Type: TokenIllegal, Literal: "&", Pos: pos}
	case '|':
		if l.peekChar() == '|' {
			l.readChar()
			l.readChar()
			return Token{Type: TokenOr, Literal: "||", Pos: pos}
		}
		l.readChar()
		return Token{Type: TokenIllegal, Literal: "|", Pos: pos}
	case '"':
		str, err := l.readString()
		if err != nil {
			return Token{Type: TokenIllegal, Literal: str, Pos: pos}
		}
		return Token{Type: TokenString, Literal: str, Pos: pos}
	default:
		if isDigit(l.ch) {
			return l.readNumber(pos)
		}
		if isLetter(l.ch) {
			ident := l.readIdentifier()
			tokType := LookupIdent(ident)
			return Token{Type: tokType, Literal: ident, Pos: pos}
		}
		ch := l.ch
		l.readChar()
		return Token{Type: TokenIllegal, Literal: string(ch), Pos: pos}
	}
}

func (l *Lexer) readIdentifier() string {
	start := l.pos
	for isLetter(l.ch) || isDigit(l.ch) || l.ch == '_' {
		l.readChar()
	}
	return l.input[start:l.pos]
}

func (l *Lexer) readNumber(pos Pos) Token {
	start := l.pos
	isDecimal := false

	for isDigit(l.ch) {
		l.readChar()
	}
	if l.ch == '.' && isDigit(l.peekChar()) {
		isDecimal = true
		l.readChar() // consume '.'
		for isDigit(l.ch) {
			l.readChar()
		}
	}
	if l.ch == 'd' || l.ch == 'D' {
		isDecimal = true
		l.readChar() // consume 'd'
	}
	numStr := l.input[start:l.pos]
	numStr = strings.TrimSuffix(strings.TrimSuffix(numStr, "d"), "D")
	if isDecimal {
		return Token{Type: TokenDecimal, Literal: numStr, Pos: pos}
	}
	return Token{Type: TokenInt, Literal: numStr, Pos: pos}
}

func (l *Lexer) readString() (string, error) {
	l.readChar() // consume opening "
	var sb strings.Builder
	for {
		if l.ch == 0 || l.ch == '\n' {
			return sb.String(), fmt.Errorf("unterminated string literal")
		}
		if l.ch == '"' {
			l.readChar() // consume closing "
			break
		}
		if l.ch == '\\' {
			l.readChar()
			switch l.ch {
			case 'n':
				sb.WriteByte('\n')
			case 't':
				sb.WriteByte('\t')
			case 'r':
				sb.WriteByte('\r')
			case '"':
				sb.WriteByte('"')
			case '\\':
				sb.WriteByte('\\')
			default:
				sb.WriteRune(l.ch)
			}
			l.readChar()
			continue
		}
		sb.WriteRune(l.ch)
		l.readChar()
	}
	return sb.String(), nil
}

func isLetter(ch rune) bool {
	return unicode.IsLetter(ch) || ch == '_'
}

func isDigit(ch rune) bool {
	return unicode.IsDigit(ch)
}
