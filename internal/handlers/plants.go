package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"iag-mes/backend/internal/store"
)

func (a *API) ListPlants(c *gin.Context) {
	items, err := a.Store.ListPlants(c.Request.Context())
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (a *API) CreatePlant(c *gin.Context) {
	var body store.Plant
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	item, err := a.Store.CreatePlant(c.Request.Context(), body)
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusCreated, item)
}

func (a *API) GetPlant(c *gin.Context) {
	item, err := a.Store.GetPlantByCode(c.Request.Context(), c.Param("code"))
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, item)
}

func (a *API) ListSections(c *gin.Context) {
	items, err := a.Store.ListSections(c.Request.Context(), c.Query("plant"))
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (a *API) CreateSection(c *gin.Context) {
	var body store.Section
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	item, err := a.Store.CreateSection(c.Request.Context(), c.Param("code"), body)
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusCreated, item)
}

func (a *API) ListAssets(c *gin.Context) {
	items, err := a.Store.ListAssets(c.Request.Context(), store.AssetFilter{
		PlantCode: c.Query("plant"),
		Category:  c.Query("category"),
		Status:    c.Query("status"),
	})
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "total": len(items)})
}

func (a *API) GetAsset(c *gin.Context) {
	item, err := a.Store.GetAssetByTag(c.Request.Context(), c.Param("tag"))
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, item)
}

func (a *API) CreateAsset(c *gin.Context) {
	var body struct {
		SectionID uuid.UUID `json:"section_id" binding:"required"`
		store.Asset
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	item, err := a.Store.CreateAsset(c.Request.Context(), body.SectionID, body.Asset)
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusCreated, item)
}

func (a *API) PatchAsset(c *gin.Context) {
	var patch store.AssetPatch
	if err := c.ShouldBindJSON(&patch); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	item, err := a.Store.PatchAsset(c.Request.Context(), c.Param("tag"), patch)
	if err != nil {
		writeStoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, item)
}
