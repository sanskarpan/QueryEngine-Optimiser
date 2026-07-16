package storage

import (
	"fmt"
	"math/rand"

	"github.com/query-engine/query-engine/internal/catalog"
)

// Seed populates the catalog and storage with deterministic seed data.
// Uses a fixed random seed (42) for reproducibility.
func Seed(cat *catalog.Catalog, store *Storage) error {
	// Register tables in catalog
	if err := registerSeedSchema(cat); err != nil {
		return err
	}
	// Insert data into storage
	if err := insertSeedData(store); err != nil {
		return err
	}
	return nil
}

// Reset drops and re-creates all seed tables.
func Reset(cat *catalog.Catalog, store *Storage) error {
	for _, name := range []string{"customers", "products", "orders"} {
		cat.Drop(name)
		store.DropTable(name)
	}
	return Seed(cat, store)
}

func registerSeedSchema(cat *catalog.Catalog) error {
	tables := []*catalog.Table{
		{
			Name: "customers",
			Columns: []catalog.Column{
				{Name: "id", Type: catalog.TypeInt, PK: true, Index: 0},
				{Name: "name", Type: catalog.TypeText, Index: 1},
				{Name: "email", Type: catalog.TypeText, Index: 2},
				{Name: "country", Type: catalog.TypeText, Index: 3},
				{Name: "created_at", Type: catalog.TypeText, Index: 4},
			},
		},
		{
			Name: "products",
			Columns: []catalog.Column{
				{Name: "id", Type: catalog.TypeInt, PK: true, Index: 0},
				{Name: "name", Type: catalog.TypeText, Index: 1},
				{Name: "category", Type: catalog.TypeText, Index: 2},
				{Name: "price", Type: catalog.TypeFloat, Index: 3},
				{Name: "stock_quantity", Type: catalog.TypeInt, Index: 4},
			},
		},
		{
			Name: "orders",
			Columns: []catalog.Column{
				{Name: "id", Type: catalog.TypeInt, PK: true, Index: 0},
				{Name: "customer_id", Type: catalog.TypeInt, Index: 1},
				{Name: "product_id", Type: catalog.TypeInt, Index: 2},
				{Name: "quantity", Type: catalog.TypeInt, Index: 3},
				{Name: "amount", Type: catalog.TypeFloat, Index: 4},
				{Name: "status", Type: catalog.TypeText, Index: 5},
				{Name: "created_at", Type: catalog.TypeText, Index: 6},
			},
		},
	}

	for _, t := range tables {
		if err := cat.Register(t); err != nil {
			return fmt.Errorf("seed: %w", err)
		}
	}
	return nil
}

func insertSeedData(store *Storage) error {
	rng := rand.New(rand.NewSource(42))

	// Create tables in storage
	for _, name := range []string{"customers", "products", "orders"} {
		if err := store.CreateTable(name); err != nil {
			return err
		}
	}

	customersTable := store.MustGetTable("customers")
	productsTable := store.MustGetTable("products")
	ordersTable := store.MustGetTable("orders")

	// Seed customers (100 rows)
	countries := []string{"US", "UK", "DE", "FR", "JP"}
	firstNames := []string{"Alice", "Bob", "Carol", "Dave", "Eve", "Frank", "Grace", "Hank", "Iris", "Jack"}
	lastNames := []string{"Smith", "Jones", "Williams", "Brown", "Taylor", "Wilson", "Davies", "Evans", "Thomas", "Roberts"}

	for i := 1; i <= 100; i++ {
		first := firstNames[rng.Intn(len(firstNames))]
		last := lastNames[rng.Intn(len(lastNames))]
		name := fmt.Sprintf("%s %s", first, last)
		email := fmt.Sprintf("%s.%s%d@example.com",
			lowercase(first), lowercase(last), i)
		country := countries[rng.Intn(len(countries))]
		createdAt := fmt.Sprintf("2023-%02d-%02d", 1+rng.Intn(12), 1+rng.Intn(28))

		customersTable.Insert(Tuple{Values: []catalog.Value{
			catalog.IntValue(int64(i)),
			catalog.TextValue(name),
			catalog.TextValue(email),
			catalog.TextValue(country),
			catalog.TextValue(createdAt),
		}})
	}

	// Seed products (50 rows)
	categories := []string{"Electronics", "Clothing", "Books", "Food", "Sports"}
	productNames := []string{"Widget", "Gadget", "Thingamajig", "Doohickey", "Gizmo",
		"Device", "Tool", "Instrument", "Apparatus", "Contraption"}

	for i := 1; i <= 50; i++ {
		cat := categories[rng.Intn(len(categories))]
		pname := fmt.Sprintf("%s %s %d", productNames[rng.Intn(len(productNames))], cat, i)
		price := 10.0 + rng.Float64()*490.0
		price = float64(int(price*100)) / 100 // 2 decimal places
		stock := 1 + rng.Intn(500)

		productsTable.Insert(Tuple{Values: []catalog.Value{
			catalog.IntValue(int64(i)),
			catalog.TextValue(pname),
			catalog.TextValue(cat),
			catalog.FloatValue(price),
			catalog.IntValue(int64(stock)),
		}})
	}

	// Seed orders (1000 rows) — status distribution: 60% shipped, 20% processing, 10% pending, 10% cancelled
	statuses := []string{"shipped", "shipped", "shipped", "shipped", "shipped", "shipped",
		"processing", "processing",
		"pending",
		"cancelled"}

	for i := 1; i <= 1000; i++ {
		customerID := 1 + rng.Intn(100)
		productID := 1 + rng.Intn(50)
		qty := 1 + rng.Intn(10)
		// amount = qty * some base price (simplified)
		amount := float64(qty) * (10.0 + rng.Float64()*100.0)
		amount = float64(int(amount*100)) / 100
		status := statuses[rng.Intn(len(statuses))]
		createdAt := fmt.Sprintf("2024-%02d-%02d", 1+rng.Intn(12), 1+rng.Intn(28))

		ordersTable.Insert(Tuple{Values: []catalog.Value{
			catalog.IntValue(int64(i)),
			catalog.IntValue(int64(customerID)),
			catalog.IntValue(int64(productID)),
			catalog.IntValue(int64(qty)),
			catalog.FloatValue(amount),
			catalog.TextValue(status),
			catalog.TextValue(createdAt),
		}})
	}

	return nil
}

func lowercase(s string) string {
	b := make([]byte, len(s))
	for i := range s {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 32
		}
		b[i] = c
	}
	return string(b)
}
