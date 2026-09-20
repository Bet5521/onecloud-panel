package api

import (
	"net/http"
	"strconv"

	"onecloud-panel/internal/recipes"
)

func (a *API) listRecipes(w http.ResponseWriter, r *http.Request) {
	list := a.recipes.ListAll()

	// 可选：携带节点架构与方式兼容性
	arch := r.URL.Query().Get("arch")
	if v := r.URL.Query().Get("node_id"); v != "" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil {
			if n, err := a.nodes.Get(id); err == nil {
				arch = n.Arch
			}
		}
	}

	type recipeView struct {
		*recipes.Recipe
		Compatibility []recipes.MethodCompat `json:"compatibility,omitempty"`
	}
	out := make([]recipeView, 0, len(list))
	for _, rp := range list {
		view := recipeView{Recipe: rp}
		if arch != "" {
			view.Compatibility = rp.Compatibility(arch)
		}
		out = append(out, view)
	}
	writeJSON(w, map[string]any{"items": out, "total": len(out)})
}

func (a *API) getRecipe(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	rp, ok := a.recipes.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "配方不存在")
		return
	}
	writeJSON(w, rp)
}
