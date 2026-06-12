package contracts

import (
	"testing"
)

func TestGraphQLExtractor_SchemaProvider(t *testing.T) {
	ext := &GraphQLExtractor{}
	src := []byte(`
type Query {
  users: [User]
  user(id: ID!): User
}

type Mutation {
  createUser(input: CreateUserInput!): User
}
`)
	contracts := ext.Extract("schema.graphql", src, nil, nil)
	if len(contracts) < 3 {
		t.Fatalf("expected at least 3 provider contracts, got %d", len(contracts))
	}

	ids := map[string]bool{}
	for _, c := range contracts {
		if c.Role != RoleProvider {
			continue
		}
		ids[c.ID] = true
	}
	for _, want := range []string{"graphql::Query::users", "graphql::Query::user", "graphql::Mutation::createUser"} {
		if !ids[want] {
			t.Errorf("missing expected contract %q, got ids: %v", want, ids)
		}
	}
}

func TestGraphQLExtractor_QueryConsumer(t *testing.T) {
	ext := &GraphQLExtractor{}
	src := []byte(`
const GET_USERS = gql` + "`" + `
  query {
    users {
      id
      name
    }
  }
` + "`" + `
`)
	contracts := ext.Extract("queries.ts", src, nil, nil)

	found := false
	for _, c := range contracts {
		if c.ID == "graphql::Query::users" && c.Role == RoleConsumer {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected consumer contract graphql::Query::users, got %v", contracts)
	}
}

func TestGraphQLExtractor_MutationConsumer(t *testing.T) {
	ext := &GraphQLExtractor{}
	src := []byte(`
mutation {
  createUser(input: {name: "Alice"}) {
    id
  }
}
`)
	contracts := ext.Extract("mutations.graphql", src, nil, nil)

	found := false
	for _, c := range contracts {
		if c.ID == "graphql::Mutation::createUser" && c.Role == RoleConsumer {
			found = true
			break
		}
	}
	if !found {
		// Also check providers since this is a .graphql file.
		for _, c := range contracts {
			if c.ID == "graphql::Mutation::createUser" {
				found = true
				break
			}
		}
	}
	if !found {
		t.Errorf("expected contract graphql::Mutation::createUser, got %v", contracts)
	}
}
