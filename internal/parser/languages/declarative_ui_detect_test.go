package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestComposeDetect(t *testing.T) {
	src := []byte(`import androidx.compose.runtime.Composable

@Composable
fun Greeting(name: String) {
    Text(text = name)
}

fun plainFn() {}
`)
	res, err := NewKotlinExtractor().Extract("App.kt", src)
	require.NoError(t, err)
	assert.Equal(t, "compose", uiComp(res.Nodes, "Greeting"))
	assert.Equal(t, "function", componentKind(res.Nodes, "Greeting"))
	assert.Equal(t, "", uiComp(res.Nodes, "plainFn"), "a plain function is not a component")
}

func TestFlutterDetect(t *testing.T) {
	src := []byte(`import 'package:flutter/material.dart';

class MyWidget extends StatelessWidget {
  Widget build(BuildContext context) => Container();
}

class MyStateful extends StatefulWidget {
  State<MyStateful> createState() => _S();
}

class Plain {}
`)
	res, err := NewDartExtractor().Extract("app.dart", src)
	require.NoError(t, err)

	w := nodeByName(res.Nodes, "MyWidget")
	require.NotNil(t, w)
	assert.Equal(t, "flutter", w.Meta["ui_component"])
	assert.Equal(t, "stateless", w.Meta["component_kind"])
	assert.Equal(t, "class", w.Meta["type_flavor"])

	sf := nodeByName(res.Nodes, "MyStateful")
	require.NotNil(t, sf)
	assert.Equal(t, "flutter", sf.Meta["ui_component"])
	assert.Equal(t, "stateful", sf.Meta["component_kind"])

	assert.Equal(t, "", uiComp(res.Nodes, "Plain"), "a plain class is not a Flutter widget")
}

func TestAngularDetect(t *testing.T) {
	src := []byte(`import { Component } from '@angular/core'

@Component({ selector: 'app-root', template: '<div></div>' })
class AppComponent {}

class Plain {}
`)
	res, err := NewTypeScriptExtractor().Extract("app.ts", src)
	require.NoError(t, err)
	assert.Equal(t, "angular", uiComp(res.Nodes, "AppComponent"))
	assert.Equal(t, "class", componentKind(res.Nodes, "AppComponent"))
	assert.Equal(t, "", uiComp(res.Nodes, "Plain"), "a plain class is not an Angular component")
}
