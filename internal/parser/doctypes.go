package parser

// Document type definitions and their extraction prompts.

type DocType string

const (
	DocTypeInvoice       DocType = "invoice"
	DocTypeReceipt       DocType = "receipt"
	DocTypeBankStatement DocType = "bank_statement"
	DocTypeContract      DocType = "contract"
	DocTypeIDDocument    DocType = "id_document"
	DocTypeTaxForm       DocType = "tax_form"
	DocTypeBusinessCard  DocType = "business_card"
)

// ReceiptData represents extracted receipt information.
type ReceiptData struct {
	MerchantName    string       `json:"merchant_name"`
	MerchantAddress string       `json:"merchant_address"`
	MerchantPhone   string       `json:"merchant_phone"`
	Date            string       `json:"date"`
	Time            string       `json:"time"`
	Items           []ReceiptItem `json:"items"`
	Subtotal        float64      `json:"subtotal"`
	Tax             float64      `json:"tax"`
	Tip             float64      `json:"tip"`
	Total           float64      `json:"total"`
	PaymentMethod   string       `json:"payment_method"`
	CardLastFour    string       `json:"card_last_four"`
	Currency        string       `json:"currency"`
	Category        string       `json:"category"`
	Confidence      float64      `json:"confidence"`
}

type ReceiptItem struct {
	Description string  `json:"description"`
	Quantity    float64 `json:"quantity"`
	Price       float64 `json:"price"`
}

// BankStatementData represents extracted bank statement information.
type BankStatementData struct {
	BankName       string              `json:"bank_name"`
	AccountHolder  string              `json:"account_holder"`
	AccountNumber  string              `json:"account_number"`
	StatementDate  string              `json:"statement_date"`
	PeriodStart    string              `json:"period_start"`
	PeriodEnd      string              `json:"period_end"`
	OpeningBalance float64             `json:"opening_balance"`
	ClosingBalance float64             `json:"closing_balance"`
	TotalCredits   float64             `json:"total_credits"`
	TotalDebits    float64             `json:"total_debits"`
	Currency       string              `json:"currency"`
	Transactions   []BankTransaction   `json:"transactions"`
	Confidence     float64             `json:"confidence"`
}

type BankTransaction struct {
	Date        string  `json:"date"`
	Description string  `json:"description"`
	Amount      float64 `json:"amount"`
	Type        string  `json:"type"` // "credit" or "debit"
	Balance     float64 `json:"balance"`
}

// ContractData represents extracted contract information.
type ContractData struct {
	Title          string           `json:"title"`
	ContractType   string           `json:"contract_type"`
	Parties        []ContractParty  `json:"parties"`
	EffectiveDate  string           `json:"effective_date"`
	ExpirationDate string           `json:"expiration_date"`
	Value          float64          `json:"value"`
	Currency       string           `json:"currency"`
	KeyClauses     []string         `json:"key_clauses"`
	GoverningLaw   string           `json:"governing_law"`
	SignatureCount int              `json:"signature_count"`
	Confidence     float64          `json:"confidence"`
}

type ContractParty struct {
	Name  string `json:"name"`
	Role  string `json:"role"`
	Email string `json:"email"`
}

// IDDocumentData represents extracted ID document information.
type IDDocumentData struct {
	DocumentType   string  `json:"document_type"` // "passport", "drivers_license", "national_id"
	DocumentNumber string  `json:"document_number"`
	FirstName      string  `json:"first_name"`
	LastName       string  `json:"last_name"`
	DateOfBirth    string  `json:"date_of_birth"`
	Nationality    string  `json:"nationality"`
	Gender         string  `json:"gender"`
	IssueDate      string  `json:"issue_date"`
	ExpiryDate     string  `json:"expiry_date"`
	IssuingCountry string  `json:"issuing_country"`
	MRZ            string  `json:"mrz"`
	Confidence     float64 `json:"confidence"`
}

// TaxFormData represents extracted tax form information.
type TaxFormData struct {
	FormType       string         `json:"form_type"`
	TaxYear        string         `json:"tax_year"`
	FilingDate     string         `json:"filing_date"`
	TaxpayerName   string         `json:"taxpayer_name"`
	TaxpayerID     string         `json:"taxpayer_id"`
	GrossIncome    float64        `json:"gross_income"`
	TotalDeductions float64       `json:"total_deductions"`
	TaxableIncome  float64        `json:"taxable_income"`
	TaxOwed        float64        `json:"tax_owed"`
	TaxPaid        float64        `json:"tax_paid"`
	Refund         float64        `json:"refund"`
	Currency       string         `json:"currency"`
	LineItems      []TaxLineItem  `json:"line_items"`
	Confidence     float64        `json:"confidence"`
}

type TaxLineItem struct {
	Label  string  `json:"label"`
	Amount float64 `json:"amount"`
}

// BusinessCardData represents extracted business card information.
type BusinessCardData struct {
	FullName    string   `json:"full_name"`
	JobTitle    string   `json:"job_title"`
	Company     string   `json:"company"`
	Email       string   `json:"email"`
	Phone       string   `json:"phone"`
	Mobile      string   `json:"mobile"`
	Website     string   `json:"website"`
	Address     string   `json:"address"`
	LinkedIn    string   `json:"linkedin"`
	Twitter     string   `json:"twitter"`
	Confidence  float64  `json:"confidence"`
}

// Prompts for each document type

var docTypePrompts = map[DocType]string{
	DocTypeReceipt: `You are a precise receipt data extraction engine. Extract all data from the receipt.

Return ONLY valid JSON (no markdown, no backticks):
{
  "merchant_name": "string",
  "merchant_address": "string",
  "merchant_phone": "string",
  "date": "YYYY-MM-DD",
  "time": "HH:MM",
  "items": [{"description": "string", "quantity": number, "price": number}],
  "subtotal": number,
  "tax": number,
  "tip": number,
  "total": number,
  "payment_method": "cash/card/other",
  "card_last_four": "string",
  "currency": "USD/EUR/etc",
  "category": "food/grocery/gas/retail/other",
  "confidence": number 0-1
}
Use "" for missing strings, 0 for missing numbers.`,

	DocTypeBankStatement: `You are a precise bank statement extraction engine. Extract all data.

Return ONLY valid JSON (no markdown, no backticks):
{
  "bank_name": "string",
  "account_holder": "string",
  "account_number": "string (last 4 digits only for security)",
  "statement_date": "YYYY-MM-DD",
  "period_start": "YYYY-MM-DD",
  "period_end": "YYYY-MM-DD",
  "opening_balance": number,
  "closing_balance": number,
  "total_credits": number,
  "total_debits": number,
  "currency": "USD/EUR/etc",
  "transactions": [{"date": "YYYY-MM-DD", "description": "string", "amount": number, "type": "credit/debit", "balance": number}],
  "confidence": number 0-1
}
Use "" for missing strings, 0 for missing numbers. Only show last 4 digits of account numbers.`,

	DocTypeContract: `You are a precise contract analysis engine. Extract key information.

Return ONLY valid JSON (no markdown, no backticks):
{
  "title": "string",
  "contract_type": "employment/nda/service/lease/sales/other",
  "parties": [{"name": "string", "role": "string", "email": "string"}],
  "effective_date": "YYYY-MM-DD",
  "expiration_date": "YYYY-MM-DD",
  "value": number,
  "currency": "USD/EUR/etc",
  "key_clauses": ["string summary of important clauses"],
  "governing_law": "string (jurisdiction)",
  "signature_count": number,
  "confidence": number 0-1
}
Use "" for missing strings, 0 for missing numbers.`,

	DocTypeIDDocument: `You are a precise ID document extraction engine. Extract visible information.

Return ONLY valid JSON (no markdown, no backticks):
{
  "document_type": "passport/drivers_license/national_id/other",
  "document_number": "string",
  "first_name": "string",
  "last_name": "string",
  "date_of_birth": "YYYY-MM-DD",
  "nationality": "string",
  "gender": "M/F/X",
  "issue_date": "YYYY-MM-DD",
  "expiry_date": "YYYY-MM-DD",
  "issuing_country": "string",
  "mrz": "string (machine readable zone if visible)",
  "confidence": number 0-1
}
Use "" for missing strings.`,

	DocTypeTaxForm: `You are a precise tax form extraction engine. Extract all financial data.

Return ONLY valid JSON (no markdown, no backticks):
{
  "form_type": "string (e.g. W-2, 1099, Modelo 303, etc.)",
  "tax_year": "YYYY",
  "filing_date": "YYYY-MM-DD",
  "taxpayer_name": "string",
  "taxpayer_id": "string",
  "gross_income": number,
  "total_deductions": number,
  "taxable_income": number,
  "tax_owed": number,
  "tax_paid": number,
  "refund": number,
  "currency": "USD/EUR/etc",
  "line_items": [{"label": "string", "amount": number}],
  "confidence": number 0-1
}
Use "" for missing strings, 0 for missing numbers.`,

	DocTypeBusinessCard: `You are a precise business card extraction engine. Extract all contact information.

Return ONLY valid JSON (no markdown, no backticks):
{
  "full_name": "string",
  "job_title": "string",
  "company": "string",
  "email": "string",
  "phone": "string",
  "mobile": "string",
  "website": "string",
  "address": "string",
  "linkedin": "string",
  "twitter": "string",
  "confidence": number 0-1
}
Use "" for missing strings.`,
}
