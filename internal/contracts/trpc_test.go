package contracts

import "testing"

// trpcProviderFixture is a server-side router definition: a
// createTRPCRouter assigned to userRouter with two procedures.
const trpcProviderFixture = `
import { createTRPCRouter, publicProcedure } from "../trpc";
import { z } from "zod";

export const userRouter = createTRPCRouter({
  getUser: publicProcedure
    .input(z.object({ id: z.string() }))
    .query(({ input }) => {
      return db.user.find(input.id);
    }),
  createUser: publicProcedure
    .input(z.object({ name: z.string() }))
    .mutation(({ input }) => {
      return db.user.create(input);
    }),
});
`

// trpcConsumerFixture is a client-side React component calling the same
// procedures through the typed proxy.
const trpcConsumerFixture = `
import { trpc } from "../utils/trpc";

export function UserProfile() {
  const { data } = trpc.userRouter.getUser.useQuery({ id: "1" });
  const create = trpc.userRouter.createUser.useMutation();
  return data;
}
`

func hasTRPCContract(cs []Contract, id string, role Role) bool {
	for _, c := range cs {
		if c.ID == id && c.Role == role && c.Type == ContractTRPC {
			return true
		}
	}
	return false
}

func TestTRPCExtractor_Provider(t *testing.T) {
	got := (&TRPCExtractor{}).Extract("server/routers/user.ts", []byte(trpcProviderFixture), nil, nil)
	if !hasTRPCContract(got, "trpc::userRouter.getUser", RoleProvider) {
		t.Fatalf("expected provider trpc::userRouter.getUser; got %+v", got)
	}
	if !hasTRPCContract(got, "trpc::userRouter.createUser", RoleProvider) {
		t.Errorf("expected provider trpc::userRouter.createUser; got %+v", got)
	}
	// No consumers should be minted from a pure router definition.
	for _, c := range got {
		if c.Role == RoleConsumer {
			t.Errorf("router definition should not produce a consumer: %+v", c)
		}
	}
}

func TestTRPCExtractor_Consumer(t *testing.T) {
	got := (&TRPCExtractor{}).Extract("app/UserProfile.tsx", []byte(trpcConsumerFixture), nil, nil)
	if !hasTRPCContract(got, "trpc::userRouter.getUser", RoleConsumer) {
		t.Fatalf("expected consumer trpc::userRouter.getUser; got %+v", got)
	}
	if !hasTRPCContract(got, "trpc::userRouter.createUser", RoleConsumer) {
		t.Errorf("expected consumer trpc::userRouter.createUser; got %+v", got)
	}
	for _, c := range got {
		if c.Role == RoleProvider {
			t.Errorf("client usage should not produce a provider: %+v", c)
		}
	}
}

// TestTRPCExtractor_ProxyClientBase verifies that a createTRPCProxyClient
// variable is recognised as a consumer base, not just the conventional
// `trpc` proxy.
func TestTRPCExtractor_ProxyClientBase(t *testing.T) {
	src := `
import { createTRPCProxyClient } from "@trpc/client";
const client = createTRPCProxyClient<AppRouter>({ links: [] });

async function run() {
  const u = await client.userRouter.getUser.query({ id: "1" });
  return u;
}
`
	got := (&TRPCExtractor{}).Extract("client.ts", []byte(src), nil, nil)
	if !hasTRPCContract(got, "trpc::userRouter.getUser", RoleConsumer) {
		t.Fatalf("expected consumer trpc::userRouter.getUser from proxy client; got %+v", got)
	}
}

// TestTRPCMatch_PairsProviderConsumer is the end-to-end canonicalization
// check: a provider and a consumer extracted independently must share a
// byte-identical ID so the matcher pairs them.
func TestTRPCMatch_PairsProviderConsumer(t *testing.T) {
	ex := &TRPCExtractor{}
	providers := ex.Extract("server/routers/user.ts", []byte(trpcProviderFixture), nil, nil)
	consumers := ex.Extract("app/UserProfile.tsx", []byte(trpcConsumerFixture), nil, nil)

	reg := NewRegistry()
	for _, c := range providers {
		reg.Add(c)
	}
	for _, c := range consumers {
		reg.Add(c)
	}

	res := Match(reg)
	paired := false
	for _, link := range res.Matched {
		if link.ContractID == "trpc::userRouter.getUser" &&
			link.Provider.Role == RoleProvider && link.Consumer.Role == RoleConsumer {
			paired = true
		}
	}
	if !paired {
		t.Fatalf("matcher did not pair trpc::userRouter.getUser; matched=%+v orphanP=%+v orphanC=%+v",
			res.Matched, res.OrphanProviders, res.OrphanConsumers)
	}
}

// TestTRPCRegisteredAsRouteFramework asserts the framework appears in the
// registry that `analyze route_frameworks` enumerates.
func TestTRPCRegisteredAsRouteFramework(t *testing.T) {
	var pass FrameworkRoutePass
	for _, p := range RegisteredFrameworkRoutePasses() {
		if p.Name() == "trpc" {
			pass = p
			break
		}
	}
	if pass == nil {
		t.Fatal("trpc is not registered as a route framework")
	}
	langs := pass.Languages()
	found := false
	for _, l := range langs {
		if l == "typescript" {
			found = true
		}
	}
	if !found {
		t.Errorf("trpc route framework should list typescript; got %v", langs)
	}
}

// TestTRPCExtractor_NestedSubRouterNotProcedure documents the v1 nested
// handling: a sub-router mounted by reference or inline is not emitted as
// a leaf procedure of the parent router.
func TestTRPCExtractor_NestedSubRouterNotProcedure(t *testing.T) {
	src := `
export const appRouter = createTRPCRouter({
  user: userRouter,
  post: t.router({ list: publicProcedure.query(() => []) }),
  health: publicProcedure.query(() => "ok"),
});
`
	got := (&TRPCExtractor{}).Extract("server/root.ts", []byte(src), nil, nil)
	for _, c := range got {
		if c.ID == "trpc::appRouter.user" || c.ID == "trpc::appRouter.post" {
			t.Errorf("sub-router mount must not be a procedure: %s", c.ID)
		}
	}
	// A real leaf procedure on the parent is still emitted.
	if !hasTRPCContract(got, "trpc::appRouter.health", RoleProvider) {
		t.Errorf("expected leaf procedure trpc::appRouter.health; got %+v", got)
	}
}
