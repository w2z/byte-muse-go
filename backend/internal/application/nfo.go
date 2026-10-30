package application

import (
	"encoding/xml"
	"strings"
)

// nfoMetadata 是媒体库旁车 NFO 中允许进入媒体资料的只读字段。
type nfoMetadata struct {
	Title string `xml:"title"`
	Plot  string `xml:"plot"`
}

// parseNFO 只解析 XML 中的标题和简介；非法或空内容由调用方按缺失资料处理。
func parseNFO(data []byte) (nfoMetadata, error) {
	var value nfoMetadata
	if err := xml.Unmarshal(data, &value); err != nil {
		return nfoMetadata{}, err
	}
	value.Title = strings.Join(strings.Fields(value.Title), " ")
	value.Plot = strings.Join(strings.Fields(value.Plot), " ")
	return value, nil
}
