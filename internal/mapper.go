// Copyright (c) 2020, Amazon.com, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package internal ...
package internal

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"text/template"

	"github.com/Masterminds/sprig/v3"
	"github.com/awslabs/ssosync/internal/aws"
	"github.com/pkg/errors"
	google "google.golang.org/api/admin/directory/v1"
)

// UserMapper ...
type UserMapper interface {
	Map(*aws.User, *google.User) (*aws.User, error)
}

type mapper struct {
	template *template.Template
	name     string
}

var _ UserMapper = (*mapper)(nil)

func funcMap() template.FuncMap {
	f := sprig.TxtFuncMap()
	delete(f, "env")
	delete(f, "expandenv")

	// Add some extra functionality
	extra := template.FuncMap{
		"include": func(string, interface{}) string { return "not implemented" },
		"tpl":     func(string, interface{}) interface{} { return "not implemented" },
	}

	for k, v := range extra {
		f[k] = v
	}

	return f
}

const recursionMaxNums = 1000

// 'include' needs to be defined in the scope of a 'tpl' template as
// well as regular file-loaded templates.
func includeFun(t *template.Template, includedNames map[string]int) func(string, interface{}) (string, error) {
	return func(name string, data interface{}) (string, error) {
		var buf strings.Builder
		if v, ok := includedNames[name]; ok {
			if v > recursionMaxNums {
				return "", errors.Wrapf(fmt.Errorf("unable to execute template"), "rendering template has a nested reference name: %s", name)
			}
			includedNames[name]++
		} else {
			includedNames[name] = 1
		}
		err := t.ExecuteTemplate(&buf, name, data)
		includedNames[name]--
		return buf.String(), err
	}
}

func listFindFirstFun(list interface{}, search map[string]interface{}) interface{} {
	tp := reflect.TypeOf(list).Kind()
	switch tp {
	case reflect.Slice, reflect.Array:
		l2 := reflect.ValueOf(list)

		l := l2.Len()
		if l == 0 {
			return nil
		}

		for index := 0; index < l; index++ {
			elem := l2.Index(index).Interface()
			if interfaceFilterMatch(elem, search) {
				return elem
			}
		}
		return nil
	default:
		return nil
	}
}

func interfaceFilterMatch(element interface{}, search map[string]interface{}) bool {
	s := reflect.ValueOf(element)
	for fieldName, searchValue := range search {
		fieldValue := s.FieldByName(fieldName)
		if !fieldValue.Equal(reflect.ValueOf(searchValue)) {
			return false
		}
	}
	return true
}

// NewMapper ...
func NewMapper(userTemplate string) (UserMapper, error) {
	t := template.New("gotpl")
	funcMap := funcMap()
	includedNames := make(map[string]int)

	funcMap["include"] = includeFun(t, includedNames)
	funcMap["listFindFirst"] = listFindFirstFun
	t.Funcs(funcMap)
	templateName := "user"

	templates := map[string]string{
		"_user": `
{{- define "libuser.user.tpl" -}}
{{- $userSchemas := list "urn:ietf:params:scim:schemas:core:2.0:User" -}}
{
	{{- with .Id -}}"externalId": {{ . | quote }},{{- end -}}
	"userName": {{ .PrimaryEmail | quote }},
	"name": {
		"familyName": {{ .Name.FamilyName | quote }},
		"givenName": {{ .Name.GivenName | quote }}
	},
	"displayName": {{ printf "%s %s" .Name.GivenName .Name.FamilyName | quote }},
	{{- with .Websites -}}
	{{- with listFindFirst . (dict "Primary" true) -}}
	{{- with .Value -}}"ProfileUrl": {{ .Value | quote }},{{- end -}}
	{{- end -}}
	{{- end -}}
	{{- with .Emails -}}
	{{- with default (first .) (listFindFirst . (dict "Primary" true)) -}}
	"emails": [{
		{{- with .Address -}}"value": {{ . | quote }},{{- end -}}
		"type": {{ default "work" (.Type | quote) }},
		"primary": true
	}],
	{{- end -}}
	{{- end -}}
	{{- with .Addresses -}}
	{{- with default (first .) (listFindFirst . (dict "Primary" true)) -}}
	"addresses": [{
		{{- with .StreetAddress -}}"streetAddress": {{ . | quote }},{{- end -}}
		{{- with .Locality -}}"locality": {{ . | quote }},{{- end -}}
		{{- with .Region -}}"region": {{ . | quote }},{{- end -}}
		{{- with .PostalCode -}}"postalCode": {{ . | quote }},{{- end -}}
		{{- with .Country -}}"country": {{ . | quote }},{{- end -}}
		{{- with .Formatted -}}"formatted": {{ . | quote }},{{- end -}}
		"type": {{ default "work" (.Type | quote) }},
		"primary": true
	}],
	{{- end -}}
	{{- end -}}
	{{- with .Phones -}}
	{{- with default (first .) (listFindFirst . (dict "Primary" true)) -}}
	"phones": [{
		{{- with .Value -}}"value": {{ . | quote }},{{- end -}}
		"type": {{ default "work" (.Type | quote) }}
	}],
	{{- end -}}
	{{- end -}}
	{{- with .Organizations -}}
	{{- with default (first .) (listFindFirst . (dict "Primary" true)) -}}
	"urn:ietf:params:scim:schemas:extension:enterprise:2.0:User": {
		{{- with .Name -}}"organization": {{ . | quote }},{{- end -}}
		{{- with .CostCenter -}}"costCenter": {{ . | quote }},{{- end -}}
		{{- with .Department -}}"department": {{ . | quote }},{{- end -}}
		{{- with .Domain -}}"division": {{ . | quote }},{{- end -}}
		"employeeNumber": {{ $.Id | quote }}
	},
	{{- $userSchemas = append $userSchemas "urn:ietf:params:scim:schemas:extension:enterprise:2.0:User" -}}
	{{- end -}}
	{{- end -}}
	"active": {{ not .Suspended }},
	"schemas": {{ toJson $userSchemas }}
}
{{- end -}}
{{- define "libuser.user" -}}
{{- include "libuser.util.merge" (append . "libuser.user.tpl") -}}
{{- end -}}`,
		"_util": `{{ define "libuser.util.merge" }}
{{- $top := first . -}}
{{- $overrides := default (dict) (fromJson (include (index . 1) $top)) -}}
{{- $tpl := default (dict) (fromJson (include (index . 2) $top)) -}}
{{- toJson (mergeOverwrite $tpl $overrides) -}}
{{- end -}}`,
		templateName: fmt.Sprintf(`{{- include "libuser.user" (list . "useroverride") -}}{{- define "useroverride" -}}%s{{- end -}}`, userTemplate),
	}

	for name, tpl := range templates {
		_, err := t.New(name).Parse(tpl)
		if err != nil {
			return nil, err
		}
	}

	return &mapper{
		template: t,
		name:     templateName,
	}, nil
}

// Map ...
func (m *mapper) Map(awsUser *aws.User, gUser *google.User) (*aws.User, error) {
	var buf bytes.Buffer
	err := m.template.ExecuteTemplate(&buf, m.name, gUser)
	if err != nil {
		return nil, err
	}

	data, err  := json.Marshal(awsUser)
	if err != nil {
		return nil, err
	}

	var clonedUser aws.User

	err = json.Unmarshal(data, &clonedUser)
	if err != nil {
		return nil, err
	}

	err = json.Unmarshal(buf.Bytes(), &clonedUser)
	if err != nil {
		return nil, err
	}

	return &clonedUser, nil
}