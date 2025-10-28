package jumpcloud

// see https://www.terraform.io/docs/extend/writing-custom-providers.html#implementing-a-more-complex-read

import (
	"fmt"
	"strconv"
	"strings"

	jcapiv2 "github.com/TheJumpCloud/jcapi-go/v2"
)

func flattenAttributes(attr *jcapiv2.UserGroupAttributes) []interface{} {
	return []interface{}{
		map[string]interface{}{
			"posix_groups": flattenPosixGroups(attr.PosixGroups),
			// "enable_samba": fmt.Sprintf("%t", attr.SambaEnabled),
		},
	}
}

func flattenPosixGroups(pg []jcapiv2.UserGroupAttributesPosixGroups) string {
	out := []string{}
	for _, v := range pg {
		out = append(out, fmt.Sprintf("%d:%s", v.Id, v.Name))
	}
	return strings.Join(out, ",")
}

func expandAttributes(attr interface{}) (out *jcapiv2.UserGroupAttributes, ok bool) {
	if attr == nil {
		return
	}
	// Handle list input (TypeList with MaxItems: 1)
	listAttr, ok := attr.([]interface{})
	if !ok || len(listAttr) == 0 {
		return
	}
	mapAttr, ok := listAttr[0].(map[string]interface{})
	if !ok {
		return
	}

	// var enableSamba bool
	// sambaStr, ok := mapAttr["enable_samba"].(string)
	// if ok {
	// 	enableSamba, _ = strconv.ParseBool(sambaStr)
	// }

	// TODO: empty string? nil?
	posixStr, ok := mapAttr["posix_groups"].(string)
	if !ok {
		return
	}

	groups := strings.Split(posixStr, ",")
	posixGroups := []jcapiv2.UserGroupAttributesPosixGroups{}
	for _, v := range groups {
		g := strings.Split(v, ":")
		if len(g) != 2 {
			return
		}
		id, err := strconv.ParseInt(g[0], 10, 32)
		if err != nil {
			continue
		}
		posixGroups = append(posixGroups,
			jcapiv2.UserGroupAttributesPosixGroups{
				Id: int32(id), Name: g[1],
			})
	}

	if len(posixGroups) == 0 {
		return
	}

	return &jcapiv2.UserGroupAttributes{
		PosixGroups: posixGroups,
		// SambaEnabled: enableSamba,
	}, true
}
