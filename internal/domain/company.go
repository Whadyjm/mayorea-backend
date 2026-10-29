package domain

import (
	"regexp"
	"strings"
)

// Company is the profile of the distributor running the system: how it appears
// on screens and documents. There is exactly one.
type Company struct {
	TradeName string // the name its customers know
	LegalName string // as it appears in the RIF
	RIF       string
	Phone     string
	Email     string
	Address   string
	City      string
	State     string
	// Logo is an already-reduced image as a data URL; empty when there is none.
	Logo string
}

// MaxLogoBytes bounds the stored logo (the client shrinks it to 256 px).
const MaxLogoBytes = 700 * 1024

var companyEmailRe = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)

// Clean trims every field and puts the RIF in its canonical form.
func (c Company) Clean() Company {
	c.TradeName = strings.TrimSpace(c.TradeName)
	c.LegalName = strings.TrimSpace(c.LegalName)
	c.RIF = NormalizeRIF(c.RIF)
	c.Phone = strings.TrimSpace(c.Phone)
	c.Email = strings.TrimSpace(c.Email)
	c.Address = strings.TrimSpace(c.Address)
	c.City = strings.TrimSpace(c.City)
	c.State = strings.TrimSpace(c.State)
	return c
}

// Validate checks a cleaned profile.
func (c Company) Validate() error {
	switch {
	case c.TradeName == "":
		return Invalid("Escribe el nombre con el que te conocen tus clientes.")
	case c.LegalName == "":
		return Invalid("Escribe la razón social tal como aparece en el RIF.")
	case !ValidRIF(c.RIF):
		return Invalid("El RIF debe verse así: J-12345678-9.")
	case c.Email != "" && !companyEmailRe.MatchString(c.Email):
		return Invalid("Ese correo no parece válido.")
	case c.Logo != "" && !strings.HasPrefix(c.Logo, "data:image/"):
		return Invalid("El logo debe ser una imagen.")
	case len(c.Logo) > MaxLogoBytes:
		return Invalid("El logo es demasiado pesado. Usa una imagen más liviana.")
	}
	return nil
}
