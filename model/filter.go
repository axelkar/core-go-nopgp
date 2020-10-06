package model

type Filter struct {
	Count  *int    `json:"count"`
	Search *string `json:"search"`
}
