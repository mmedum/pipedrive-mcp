package pipedrive

import "encoding/json"

// OneLine is an address or a location written as the single line a
// person says. Pipedrive v2 takes it as an object holding that line, and
// refuses a bare string with "The value is not a valid 'array'", though
// its reference gives only the object's parts. It stores the line as
// given, and the parsed parts read back null. A PATCH replaces the
// stored object whole, parts included, and an empty line is refused
// ("Field 'value' is empty"), so a OneLine cannot clear the field.
// Checked by the integration tests
// TestWrite_OrganizationAddressTakesOneLine and
// TestWrite_ActivityLocationTakesOneLine; the merge and the empty line by
// hand against v2.
// https://developers.pipedrive.com/docs/api/v1/Organizations#addOrganization
type OneLine string

// MarshalJSON writes the line as {"value": line}. A struct field of this
// type with omitempty is still left out when the line is empty, because
// encoding/json tests emptiness on the string before marshaling.
func (l OneLine) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Value string `json:"value"`
	}{string(l)})
}
