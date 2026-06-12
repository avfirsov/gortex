package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zzet/gortex/internal/graph"
)

func TestHCLExtractor_Blocks(t *testing.T) {
	src := []byte(`resource "aws_instance" "web" {
  ami           = "ami-12345"
  instance_type = "t2.micro"
}

variable "region" {
  default = "us-east-1"
}

output "instance_id" {
  value = aws_instance.web.id
}
`)
	e := NewHCLExtractor()
	result, err := e.Extract("main.tf", src)
	require.NoError(t, err)

	types := nodesOfKind(result.Nodes, graph.KindType)
	assert.GreaterOrEqual(t, len(types), 3, "should extract resource, variable, and output blocks")

	// Check that block names include labels.
	names := make(map[string]bool)
	for _, n := range types {
		names[n.Name] = true
	}
	assert.True(t, names["resource.aws_instance.web"], "should have resource.aws_instance.web")
	assert.True(t, names["variable.region"], "should have variable.region")
}

func TestHCLExtractor_ModuleAndData(t *testing.T) {
	src := []byte(`module "vpc" {
  source = "./modules/vpc"
}

data "aws_ami" "ubuntu" {
  most_recent = true
}
`)
	e := NewHCLExtractor()
	result, err := e.Extract("infra.tf", src)
	require.NoError(t, err)

	types := nodesOfKind(result.Nodes, graph.KindType)
	assert.GreaterOrEqual(t, len(types), 2, "should extract module and data blocks")
}

func TestHCLExtractor_References(t *testing.T) {
	src := []byte(`variable "region" {
  default = "us-east-1"
}

locals {
  name = "web-${var.region}"
}

data "aws_ami" "ubuntu" {
  most_recent = true
}

module "vpc" {
  source = "./modules/vpc"
}

resource "aws_instance" "web" {
  ami           = data.aws_ami.ubuntu.id
  instance_type = var.instance_type
  subnet_id     = module.vpc.subnet_ids[0]
  tags          = { Name = local.name }
}

output "ip" {
  value = aws_instance.web.private_ip
}
`)
	e := NewHCLExtractor()
	result, err := e.Extract("main.tf", src)
	require.NoError(t, err)

	// tf_address rides on each block's Meta.
	addrByName := map[string]string{}
	for _, n := range result.Nodes {
		if a, _ := n.Meta["tf_address"].(string); a != "" {
			addrByName[n.Name] = a
		}
	}
	assert.Equal(t, "aws_instance.web", addrByName["resource.aws_instance.web"])
	assert.Equal(t, "var.region", addrByName["variable.region"])
	assert.Equal(t, "data.aws_ami.ubuntu", addrByName["data.aws_ami.ubuntu"])
	assert.Equal(t, "module.vpc", addrByName["module.vpc"])

	// Each local declaration is its own addressable KindConstant node.
	var localName *graph.Node
	for _, n := range result.Nodes {
		if n.Kind == graph.KindConstant && n.Name == "local.name" {
			localName = n
		}
	}
	require.NotNil(t, localName, "locals { name = ... } should yield a local.name node")
	assert.Equal(t, "hcl::.::local.name", localName.ID)

	// Reference edges link the resource to everything it traverses.
	refs := map[string]bool{}
	for _, ed := range result.Edges {
		if ed.Kind == graph.EdgeReferences {
			refs[ed.From+" -> "+ed.To] = true
		}
	}
	web := "hcl::.::aws_instance.web"
	assert.True(t, refs[web+" -> hcl::.::data.aws_ami.ubuntu"], "web -> data.aws_ami.ubuntu")
	assert.True(t, refs[web+" -> hcl::.::var.instance_type"], "web -> var.instance_type")
	assert.True(t, refs[web+" -> hcl::.::module.vpc"], "web -> module.vpc")
	assert.True(t, refs[web+" -> hcl::.::local.name"], "web -> local.name")
	// The local's own value references the variable it interpolates.
	assert.True(t, refs["hcl::.::local.name -> hcl::.::var.region"], "local.name -> var.region")
	// The output references the resource.
	assert.True(t, refs["hcl::.::output.ip -> hcl::.::aws_instance.web"], "output.ip -> aws_instance.web")
	// Built-in scopes (count/each/self/path/terraform) never become refs.
	for k := range refs {
		assert.NotContains(t, k, "::count.")
		assert.NotContains(t, k, "::self.")
	}
}

func TestHCLExtractor_FileNode(t *testing.T) {
	src := []byte(`variable "name" {}
`)
	e := NewHCLExtractor()
	result, err := e.Extract("vars.tf", src)
	require.NoError(t, err)

	files := nodesOfKind(result.Nodes, graph.KindFile)
	assert.Equal(t, 1, len(files))
	assert.Equal(t, "vars.tf", files[0].Name)
}
