package catalog

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makeTestTable(name string) *Table {
	return &Table{
		Name: name,
		Columns: []Column{
			{Name: "id", Type: TypeInt, PK: true, Index: 0},
			{Name: "name", Type: TypeText, Nullable: true, Index: 1},
		},
	}
}

func TestCatalog_Register_Lookup(t *testing.T) {
	c := New()
	require.NoError(t, c.Register(makeTestTable("orders")))

	table, ok := c.Lookup("orders")
	assert.True(t, ok)
	assert.Equal(t, "orders", table.Name)
}

func TestCatalog_Lookup_CaseInsensitive(t *testing.T) {
	c := New()
	require.NoError(t, c.Register(makeTestTable("Orders")))

	_, ok1 := c.Lookup("orders")
	assert.True(t, ok1)
	_, ok2 := c.Lookup("ORDERS")
	assert.True(t, ok2)
}

func TestCatalog_Register_Duplicate_Error(t *testing.T) {
	c := New()
	require.NoError(t, c.Register(makeTestTable("foo")))
	err := c.Register(makeTestTable("foo"))
	assert.Error(t, err)
}

func TestCatalog_MustRegister_Duplicate_Panics(t *testing.T) {
	c := New()
	c.MustRegister(makeTestTable("foo"))
	assert.Panics(t, func() {
		c.MustRegister(makeTestTable("foo"))
	})
}

func TestCatalog_List(t *testing.T) {
	c := New()
	require.NoError(t, c.Register(makeTestTable("zebra")))
	require.NoError(t, c.Register(makeTestTable("alpha")))
	require.NoError(t, c.Register(makeTestTable("beta")))

	list := c.List()
	assert.Equal(t, []string{"alpha", "beta", "zebra"}, list)
}

func TestCatalog_Drop(t *testing.T) {
	c := New()
	require.NoError(t, c.Register(makeTestTable("tmp")))
	assert.True(t, c.Drop("tmp"))
	assert.False(t, c.Drop("tmp"))
	_, ok := c.Lookup("tmp")
	assert.False(t, ok)
}

func TestTable_FindColumn(t *testing.T) {
	table := makeTestTable("t")
	col := table.FindColumn("name")
	require.NotNil(t, col)
	assert.Equal(t, "name", col.Name)

	col = table.FindColumn("NAME") // case-insensitive
	require.NotNil(t, col)

	col = table.FindColumn("missing")
	assert.Nil(t, col)
}

// -----------------------------------------------------------------------
// Value tests
// -----------------------------------------------------------------------

func TestValue_Compare_Ints(t *testing.T) {
	tests := []struct {
		a, b     Value
		expected int
	}{
		{IntValue(1), IntValue(2), -1},
		{IntValue(2), IntValue(2), 0},
		{IntValue(3), IntValue(2), 1},
	}
	for _, tt := range tests {
		res, err := tt.a.Compare(tt.b)
		require.NoError(t, err)
		assert.Equal(t, tt.expected, res)
	}
}

func TestValue_Compare_IntFloat(t *testing.T) {
	res, err := IntValue(3).Compare(FloatValue(3.0))
	require.NoError(t, err)
	assert.Equal(t, 0, res)

	res, err = IntValue(2).Compare(FloatValue(2.5))
	require.NoError(t, err)
	assert.Equal(t, -1, res)
}

func TestValue_Compare_Text(t *testing.T) {
	res, err := TextValue("apple").Compare(TextValue("banana"))
	require.NoError(t, err)
	assert.Equal(t, -1, res)
}

func TestValue_Compare_Null(t *testing.T) {
	_, err := NullValue().Compare(IntValue(1))
	assert.ErrorIs(t, err, ErrNullComparison)
}

func TestValue_Arithmetic(t *testing.T) {
	a := IntValue(10)
	b := IntValue(3)

	sum, err := a.Add(b)
	require.NoError(t, err)
	assert.Equal(t, int64(13), sum.IntVal)

	diff, err := a.Sub(b)
	require.NoError(t, err)
	assert.Equal(t, int64(7), diff.IntVal)

	prod, err := a.Mul(b)
	require.NoError(t, err)
	assert.Equal(t, int64(30), prod.IntVal)

	quot, err := a.Div(b)
	require.NoError(t, err)
	assert.Equal(t, int64(3), quot.IntVal)

	mod, err := a.Mod(b)
	require.NoError(t, err)
	assert.Equal(t, int64(1), mod.IntVal)
}

func TestValue_Arithmetic_FloatPromotion(t *testing.T) {
	a := IntValue(10)
	b := FloatValue(3.0)

	sum, err := a.Add(b)
	require.NoError(t, err)
	assert.Equal(t, TypeFloat, sum.Type)
	assert.InDelta(t, 13.0, sum.FloatVal, 0.001)
}

func TestValue_Arithmetic_NullPropagation(t *testing.T) {
	res, err := IntValue(5).Add(NullValue())
	require.NoError(t, err)
	assert.True(t, res.IsNull)
}

func TestValue_DivisionByZero(t *testing.T) {
	_, err := IntValue(10).Div(IntValue(0))
	assert.Error(t, err)
}

func TestValue_String(t *testing.T) {
	assert.Equal(t, "42", IntValue(42).String())
	assert.Equal(t, "3.14", FloatValue(3.14).String())
	assert.Equal(t, "hello", TextValue("hello").String())
	assert.Equal(t, "true", BoolValue(true).String())
	assert.Equal(t, "NULL", NullValue().String())
}

func TestParseDataType(t *testing.T) {
	tests := []struct {
		input    string
		expected DataType
	}{
		{"INT", TypeInt},
		{"INTEGER", TypeInt},
		{"FLOAT", TypeFloat},
		{"TEXT", TypeText},
		{"VARCHAR", TypeText},
		{"VARCHAR(255)", TypeText},
		{"BOOL", TypeBool},
		{"BOOLEAN", TypeBool},
	}
	for _, tt := range tests {
		dt, err := ParseDataType(tt.input)
		require.NoError(t, err, "input: %s", tt.input)
		assert.Equal(t, tt.expected, dt, "input: %s", tt.input)
	}

	_, err := ParseDataType("JSONB")
	assert.Error(t, err)
}
