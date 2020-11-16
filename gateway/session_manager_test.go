package gateway

import (
	"net/http"
	"testing"

	"github.com/jensneuse/graphql-go-tools/pkg/graphql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TykTechnologies/tyk/user"
)

func TestSessionLimiter_DepthLimitEnabled(t *testing.T) {
	l := SessionLimiter{}

	accessDefPerField := &user.AccessDefinition{
		FieldAccessRights: []user.FieldAccessDefinition{
			{TypeName: "Query", FieldName: "countries", Limits: user.FieldLimits{MaxQueryDepth: 0}},
			{TypeName: "Query", FieldName: "continents", Limits: user.FieldLimits{MaxQueryDepth: -1}},
			{TypeName: "Mutation", FieldName: "putCountry", Limits: user.FieldLimits{MaxQueryDepth: 2}},
		},
		Limit: &user.APILimit{
			MaxQueryDepth: 0,
		},
	}

	accessDefWithGlobal := &user.AccessDefinition{
		Limit: &user.APILimit{
			MaxQueryDepth: 2,
		},
	}

	t.Run("graphqlEnabled", func(t *testing.T) {
		assert.False(t, l.DepthLimitEnabled(false, accessDefWithGlobal))
		assert.True(t, l.DepthLimitEnabled(true, accessDefWithGlobal))
	})

	t.Run("Per field", func(t *testing.T) {
		assert.True(t, l.DepthLimitEnabled(true, accessDefPerField))
		accessDefPerField.FieldAccessRights = []user.FieldAccessDefinition{}
		assert.False(t, l.DepthLimitEnabled(true, accessDefPerField))
	})

	t.Run("Global", func(t *testing.T) {
		assert.True(t, l.DepthLimitEnabled(true, accessDefWithGlobal))
		accessDefWithGlobal.Limit.MaxQueryDepth = 0
		assert.False(t, l.DepthLimitEnabled(true, accessDefWithGlobal))
	})
}

func TestSessionLimiter_DepthLimitExceeded(t *testing.T) {
	l := SessionLimiter{}
	countriesSchema, err := graphql.NewSchemaFromString(gqlCountriesSchema)
	require.NoError(t, err)

	// global depth: 4 countries depth: 3
	countriesQuery := `query TestQuery { countries { code name continent { code name countries { code name } } }}`

	// global depth: 5 countries depth: 3 continents depth: 4
	countriesContinentsQuery := `query TestQuery { 
		countries { continent { countries { name } } }
		continents { countries { continent { countries { name } } } }
	}`

	cases := []struct {
		name      string
		query     string
		accessDef *user.AccessDefinition
		result    sessionFailReason
	}{
		{
			name:  "should use global limit and exceed when no per field rights",
			query: countriesQuery,
			accessDef: &user.AccessDefinition{
				Limit:             &user.APILimit{MaxQueryDepth: 3},
				FieldAccessRights: []user.FieldAccessDefinition{},
			},
			result: sessionFailDepthLimit,
		},
		{
			name:  "should respect unlimited specific field depth limit and not exceed",
			query: countriesQuery,
			accessDef: &user.AccessDefinition{
				Limit: &user.APILimit{MaxQueryDepth: 3},
				FieldAccessRights: []user.FieldAccessDefinition{
					{
						TypeName: "Query", FieldName: "countries",
						Limits: user.FieldLimits{MaxQueryDepth: -1},
					},
				},
			},
			result: sessionFailNone,
		},
		{
			name:  "should respect higher specific field depth limit and not exceed",
			query: countriesQuery,
			accessDef: &user.AccessDefinition{
				Limit: &user.APILimit{MaxQueryDepth: 3},
				FieldAccessRights: []user.FieldAccessDefinition{
					{
						TypeName: "Query", FieldName: "countries",
						Limits: user.FieldLimits{MaxQueryDepth: 10},
					},
				},
			},
			result: sessionFailNone,
		},
		{
			name:  "should respect lower specific field depth limit and exceed",
			query: countriesQuery,
			accessDef: &user.AccessDefinition{
				Limit: &user.APILimit{MaxQueryDepth: 100},
				FieldAccessRights: []user.FieldAccessDefinition{
					{
						TypeName: "Query", FieldName: "countries",
						Limits: user.FieldLimits{MaxQueryDepth: 1},
					},
				},
			},
			result: sessionFailDepthLimit,
		},
		{
			name:  "should respect specific field depth limits and not exceed",
			query: countriesContinentsQuery,
			accessDef: &user.AccessDefinition{
				Limit: &user.APILimit{MaxQueryDepth: 1},
				FieldAccessRights: []user.FieldAccessDefinition{
					{
						TypeName: "Query", FieldName: "countries",
						Limits: user.FieldLimits{MaxQueryDepth: 3},
					},
					{
						TypeName: "Query", FieldName: "continents",
						Limits: user.FieldLimits{MaxQueryDepth: 4},
					},
				},
			},
			result: sessionFailNone,
		},
		{
			name:  "should fallback to global limit when continents limits is not specified",
			query: countriesContinentsQuery,
			accessDef: &user.AccessDefinition{
				Limit: &user.APILimit{MaxQueryDepth: 1},
				FieldAccessRights: []user.FieldAccessDefinition{
					{
						TypeName: "Query", FieldName: "countries",
						Limits: user.FieldLimits{MaxQueryDepth: 3},
					},
				},
			},
			result: sessionFailDepthLimit,
		},
		{
			name:  "should fallback to global limit when countries limits is not specified",
			query: countriesContinentsQuery,
			accessDef: &user.AccessDefinition{
				Limit: &user.APILimit{MaxQueryDepth: 1},
				FieldAccessRights: []user.FieldAccessDefinition{
					{
						TypeName: "Query", FieldName: "continents",
						Limits: user.FieldLimits{MaxQueryDepth: 4},
					},
				},
			},
			result: sessionFailDepthLimit,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := &graphql.Request{
				OperationName: "TestQuery",
				Variables:     nil,
				Query:         tc.query,
			}

			failReason := l.DepthLimitExceeded(req, tc.accessDef, countriesSchema)
			assert.Equal(t, tc.result, failReason)

		})
	}
}

func TestSessionLimiter_ForwardMessage_GraphqlLimits(t *testing.T) {
	countriesSchema, err := graphql.NewSchemaFromString(gqlCountriesSchema)
	require.NoError(t, err)

	apiSpec := BuildAPI(func(spec *APISpec) {
		spec.GraphQL.Enabled = true
		spec.GraphQL.Schema = gqlCountriesSchema
		spec.GraphQLExecutor.Schema = countriesSchema
	})[0]

	gqlReq := &graphql.Request{
		OperationName: "TestQuery",
		Variables:     nil,
		Query:         `query TestQuery { countries { code name continent { code name countries { code name } } }}`,
	}
	httpReq := &http.Request{}
	ctxSetGraphQLRequest(httpReq, gqlReq)

	session := user.NewSessionState()
	session.MaxQueryDepth = 3
	l := SessionLimiter{}

	cases := []struct {
		name   string
		rights map[string]user.AccessDefinition
		result sessionFailReason
	}{
		{
			name:   "should fail when no access rights for the given spec",
			rights: map[string]user.AccessDefinition{"any": {}},
			result: sessionFailRateLimit,
		},
		{
			name: "should take limit from access rights and fail",
			rights: map[string]user.AccessDefinition{
				apiSpec.APIID: {
					Limit: &user.APILimit{MaxQueryDepth: 1},
				},
			},
			result: sessionFailDepthLimit,
		},
		{
			name: "should take field limits from access rights and proceed",
			rights: map[string]user.AccessDefinition{
				apiSpec.APIID: {
					FieldAccessRights: []user.FieldAccessDefinition{
						{
							TypeName: "Query", FieldName: "countries",
							Limits: user.FieldLimits{MaxQueryDepth: 3},
						},
					},
				},
			},
			result: sessionFailNone,
		},
		{
			name: "should take limit from session and fail when access rights with not matching field and empty limit",
			rights: map[string]user.AccessDefinition{
				apiSpec.APIID: {
					FieldAccessRights: []user.FieldAccessDefinition{
						{
							TypeName: "Query", FieldName: "continents",
							Limits: user.FieldLimits{MaxQueryDepth: 4},
						},
					},
				},
			},
			result: sessionFailDepthLimit,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			session.SetAccessRights(tc.rights)
			failReason := l.ForwardMessage(httpReq, session, "", nil, false, false, nil, apiSpec, false)
			assert.Equal(t, tc.result, failReason)
		})
	}
}
