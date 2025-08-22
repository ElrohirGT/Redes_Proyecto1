package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/invopop/jsonschema"
)

func main() {
	client := anthropic.NewClient()

	content := "Where is San Francisco?"

	println("[user]: " + content)

	messages := []anthropic.MessageParam{
		anthropic.NewUserMessage(anthropic.NewTextBlock(content)),
	}

	toolParams := []anthropic.ToolParam{
		{
			Name:        "get_coordinates",
			Description: anthropic.String("Accepts a place as an address, then returns the latitude and longitude coordinates."),
			InputSchema: GetCoordinatesInputSchema,
		},
	}
	tools := make([]anthropic.ToolUnionParam, len(toolParams))
	for i, toolParam := range toolParams {
		tools[i] = anthropic.ToolUnionParam{OfTool: &toolParam}
	}

	for {
		message, err := client.Messages.New(context.TODO(), anthropic.MessageNewParams{
			Model:     anthropic.ModelClaudeSonnet4_20250514,
			MaxTokens: 1024,
			Messages:  messages,
			Tools:     tools,
		})

		if err != nil {
			panic(err)
		}

		print(color("[assistant]: "))
		for _, block := range message.Content {
			switch block := block.AsAny().(type) {
			case anthropic.TextBlock:
				println(block.Text)
				println()
			case anthropic.ToolUseBlock:
				inputJSON, _ := json.Marshal(block.Input)
				println(block.Name + ": " + string(inputJSON))
				println()
			}
		}

		messages = append(messages, message.ToParam())
		toolResults := []anthropic.ContentBlockParamUnion{}

		for _, block := range message.Content {
			switch variant := block.AsAny().(type) {
			case anthropic.ToolUseBlock:
				print(color("[user (" + block.Name + ")]: "))

				var response interface{}
				switch block.Name {
				case "get_coordinates":
					var input struct {
						Location string `json:"location"`
					}

					err := json.Unmarshal([]byte(variant.JSON.Input.Raw()), &input)
					if err != nil {
						panic(err)
					}

					response = GetCoordinates(input.Location)
				}

				b, err := json.Marshal(response)
				if err != nil {
					panic(err)
				}

				println(string(b))

				toolResults = append(toolResults, anthropic.NewToolResultBlock(block.ID, string(b), false))
			}

		}
		if len(toolResults) == 0 {
			break
		}
		messages = append(messages, anthropic.NewUserMessage(toolResults...))
	}
}

type GetCoordinatesInput struct {
	Location string `json:"location" jsonschema_description:"The location to look up."`
}

var GetCoordinatesInputSchema = GenerateSchema[GetCoordinatesInput]()

type GetCoordinateResponse struct {
	Long float64 `json:"long"`
	Lat  float64 `json:"lat"`
}

func GetCoordinates(location string) GetCoordinateResponse {
	return GetCoordinateResponse{
		Long: -122.4194,
		Lat:  37.7749,
	}
}

func GenerateSchema[T any]() anthropic.ToolInputSchemaParam {
	reflector := jsonschema.Reflector{
		AllowAdditionalProperties: false,
		DoNotReference:            true,
	}
	var v T

	schema := reflector.Reflect(v)

	return anthropic.ToolInputSchemaParam{
		Properties: schema.Properties,
	}
}

func color(s string) string {
	return fmt.Sprintf("\033[1;%sm%s\033[0m", "33", s)
}
