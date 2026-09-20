package recipes

import "gopkg.in/yaml.v3"

func cloneRecipe(r *Recipe) (*Recipe, error) {
	data, err := yaml.Marshal(r)
	if err != nil {
		return nil, err
	}
	out := &Recipe{}
	if err := yaml.Unmarshal(data, out); err != nil {
		return nil, err
	}
	return out, nil
}
