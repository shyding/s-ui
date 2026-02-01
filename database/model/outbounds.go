package model

import "encoding/json"

type Outbound struct {
	Id           uint            `json:"id" form:"id" gorm:"primaryKey;autoIncrement"`
	Type         string          `json:"type" form:"type"`
	Tag          string          `json:"tag" form:"tag" gorm:"unique"`
	Options      json.RawMessage `json:"-" form:"-"`
	LandingIP    string          `json:"landingIP,omitempty" form:"landingIP"`
	Country      string          `json:"country,omitempty" form:"country"`
	Region       string          `json:"region,omitempty" form:"region"`
	City         string          `json:"city,omitempty" form:"city"`
	LastTestTime int64           `json:"lastTestTime,omitempty" form:"lastTestTime"`
}

func (o *Outbound) UnmarshalJSON(data []byte) error {
	var err error
	var raw map[string]interface{}
	if err = json.Unmarshal(data, &raw); err != nil {
		return err
	}

	// Extract fixed fields and store the rest in Options
	if val, exists := raw["id"].(float64); exists {
		o.Id = uint(val)
	}
	delete(raw, "id")
	o.Type, _ = raw["type"].(string)
	delete(raw, "type")
	o.Tag = raw["tag"].(string)
	delete(raw, "tag")

	// Remaining fields
	o.Options, err = json.MarshalIndent(raw, "", "  ")
	return err
}

// MarshalJSON customizes marshalling
func (o Outbound) MarshalJSON() ([]byte, error) {
	// Combine fixed fields and dynamic fields into one map
	combined := make(map[string]interface{})
	combined["type"] = o.Type
	combined["tag"] = o.Tag
	
	// Add location fields if they exist
	if o.LandingIP != "" {
		combined["landingIP"] = o.LandingIP
	}
	if o.Country != "" {
		combined["country"] = o.Country
	}
	if o.Region != "" {
		combined["region"] = o.Region
	}
	if o.City != "" {
		combined["city"] = o.City
	}
	if o.LastTestTime > 0 {
		combined["lastTestTime"] = o.LastTestTime
	}

	if o.Options != nil {
		var restFields map[string]json.RawMessage
		if err := json.Unmarshal(o.Options, &restFields); err != nil {
			return nil, err
		}

		for k, v := range restFields {
			combined[k] = v
		}
	}

	return json.Marshal(combined)
}
