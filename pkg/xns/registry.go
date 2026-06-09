package xns

type Event struct {
	TxID                     string `json:"txid"`
	Height                   uint64 `json:"height"`
	Amount                   uint64 `json:"amount_atomic"`
	Years                    uint64 `json:"years"`
	Name                     string `json:"name,omitempty"`
	OwnerKey                 string `json:"owner_key,omitempty"`
	Action                   string `json:"action"`
	Reason                   string `json:"reason,omitempty"`
	PreviousExpirationHeight uint64 `json:"previous_expiration_height,omitempty"`
	ExpirationHeight         uint64 `json:"expiration_height,omitempty"`
}

type Entry struct {
	Name             string   `json:"name"`
	OwnerKey         string   `json:"owner_key"`
	ExpirationHeight uint64   `json:"expiration_height"`
	FirstClaimHeight uint64   `json:"first_claim_height"`
	LastUpdateHeight uint64   `json:"last_update_height"`
	SourceTxIDs      []string `json:"source_txids"`
}

type Registry struct {
	Names map[string]Entry
}

func NewRegistry() Registry {
	return Registry{Names: make(map[string]Entry)}
}

func (r Registry) Apply(txid string, height, amount uint64, payload *Payload, invalid string) Event {
	years, err := YearsFromAmount(amount)
	if err != nil {
		return Event{TxID: txid, Height: height, Amount: amount, Action: "ignored", Reason: err.Error()}
	}
	if payload == nil {
		if invalid == "" {
			invalid = "missing XNS payload"
		}
		return Event{TxID: txid, Height: height, Amount: amount, Years: years, Action: "ignored", Reason: invalid}
	}

	name := payload.Name
	owner := payload.OwnerKey
	extension := years * BlocksPerYear
	existing, ok := r.Names[name]
	if !ok || existing.ExpirationHeight <= height {
		expires := height + extension
		r.Names[name] = Entry{
			Name:             name,
			OwnerKey:         owner,
			ExpirationHeight: expires,
			FirstClaimHeight: height,
			LastUpdateHeight: height,
			SourceTxIDs:      []string{txid},
		}
		return Event{TxID: txid, Height: height, Amount: amount, Years: years, Name: name, OwnerKey: owner, Action: "claimed", ExpirationHeight: expires}
	}
	if existing.OwnerKey != owner {
		return Event{TxID: txid, Height: height, Amount: amount, Years: years, Name: name, OwnerKey: owner, Action: "ignored", Reason: "name is already active for a different owner", PreviousExpirationHeight: existing.ExpirationHeight}
	}

	prev := existing.ExpirationHeight
	existing.ExpirationHeight += extension
	existing.LastUpdateHeight = height
	existing.SourceTxIDs = append(existing.SourceTxIDs, txid)
	r.Names[name] = existing
	return Event{TxID: txid, Height: height, Amount: amount, Years: years, Name: name, OwnerKey: owner, Action: "renewed", PreviousExpirationHeight: prev, ExpirationHeight: existing.ExpirationHeight}
}
