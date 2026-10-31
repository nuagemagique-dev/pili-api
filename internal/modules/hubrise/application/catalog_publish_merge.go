package application

import (
	"encoding/json"
	"maps"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"tsb-service/internal/modules/hubrise/domain"
	"tsb-service/pkg/hubrise"
)

// The merge: the base HubRise catalog plus the desired state give the catalog to PUT. "Owned"
// fields are overwritten from the local menu; everything else of the same element, and every element
// the shop does not own, is carried over untouched (amendment A15.4).

func money(d decimal.Decimal) string { return string(hubrise.MoneyFromDecimal(d)) }

// placeOwned merges the owned elements into a base list. The owned elements that already exist take
// the slots the owned ones had, in the desired order; new ones go right after the last slot (at the
// end when there was none); elements in drop vanish; every other element keeps its place.
func placeOwned(base []jobj, keyOf func(jobj) string, desired []jobj, drop map[string]bool) []jobj {
	want := map[string]bool{}
	for _, d := range desired {
		want[keyOf(d)] = true
	}
	lastSlot := -1
	for i, e := range base {
		if want[keyOf(e)] {
			lastSlot = i
		}
	}
	out := make([]jobj, 0, len(base)+len(desired))
	di := 0
	for i, e := range base {
		k := keyOf(e)
		switch {
		case want[k]:
			if di < len(desired) {
				out = append(out, desired[di])
				di++
			}
		case drop[k]:
		default:
			out = append(out, e)
		}
		if i == lastSlot {
			out = append(out, desired[di:]...)
			di = len(desired)
		}
	}
	if lastSlot < 0 {
		out = append(out, desired[di:]...)
	}
	return out
}

func refOf(o jobj) string { return jstr(o, "ref") }

// assemble builds the catalog to PUT.
func (p *pubPlanner) assemble() jobj {
	out := p.base.clone()
	p.mergeLists(out)
	p.mergeProducts(out)
	p.dropUnreferenced(out)
	p.mergeCategories(out)
	p.mergeVariants(out)
	return out.data()
}

// ---- option lists ----

func (p *pubPlanner) mergeLists(out *baseCatalog) {
	baseByRef := map[string]jobj{}
	for _, l := range out.lists {
		baseByRef[refOf(l)] = l
	}
	var merged []jobj
	for _, dl := range p.lists {
		src := baseByRef[dl.ref]
		if src == nil && dl.splitFrom != "" {
			src = baseByRef[dl.splitFrom]
		}
		merged = append(merged, p.mergeList(src, dl))
	}
	// Lists nothing uses any more are dropped after the SKUs are known (dropUnreferenced).
	out.lists = placeOwned(out.lists, refOf, merged, nil)
}

func (p *pubPlanner) mergeList(src jobj, dl *dList) jobj {
	l := cloneObj(src)
	l["ref"] = dl.ref
	l["name"] = dl.name
	l["min_selections"] = json.Number(strconv.Itoa(max(dl.min, 0)))
	n := len(dl.options)
	baseMultiple, _ := l["multiple_selection"].(bool)
	multiple := false
	switch {
	case dl.max > n:
		multiple = true
	case dl.max <= 1:
		multiple = false
	default:
		multiple = baseMultiple
	}
	delete(l, "max_selections")
	switch {
	case multiple && dl.max < maxMultipleSelections:
		l["max_selections"] = json.Number(strconv.Itoa(dl.max))
	case !multiple && dl.max > 0 && dl.max < n:
		l["max_selections"] = json.Number(strconv.Itoa(dl.max))
	}
	l["multiple_selection"] = multiple

	baseOpts := map[string]jobj{}
	for _, o := range jobjs(l, "options") {
		baseOpts[refOf(o)] = o
	}
	desired := make([]jobj, 0, n)
	for _, c := range dl.options {
		o := cloneObj(baseOpts[c.ref])
		o["ref"] = c.ref
		o["name"] = c.name
		o["price"] = money(c.price)
		p.writePlatformPrices(o, c.price)
		desired = append(desired, o)
	}
	l["options"] = objsToAny(placeOwned(jobjs(l, "options"), refOf, desired, p.removedOpts[dl.ref]))
	return l
}

// ---- the price lists of the platforms ----

// hiddenVariantRef is the variant no platform reads. The bridges ignore tags, so a hidden SKU is
// restricted to it to leave the menus of the platforms; the shop and the POS read the tag.
const hiddenVariantRef = "pili-hidden"

// mergeVariants makes sure the variants the prices and restrictions name exist.
func (p *pubPlanner) mergeVariants(out *baseCatalog) {
	if len(p.in.Settings.PlatformPrices) == 0 {
		return
	}
	byRef := map[string]jobj{}
	for _, v := range out.variants {
		byRef[refOf(v)] = v
	}
	put := func(ref, name string) {
		if v := byRef[ref]; v != nil {
			v["name"] = name
			return
		}
		out.variants = append(out.variants, jobj{"ref": ref, "name": name})
	}
	for _, e := range p.in.Settings.PlatformPrices {
		put(e.VariantRef, e.Name)
	}
	put(hiddenVariantRef, "Masqué")
}

// writePlatformPrices replaces the price rule of each platform on a SKU or an option. Every other
// rule is carried over.
func (p *pubPlanner) writePlatformPrices(o jobj, price decimal.Decimal) {
	platforms := p.in.Settings.PlatformPrices
	if len(platforms) == 0 {
		return
	}
	var kept []jobj
	for _, rule := range jobjs(o, "price_overrides") {
		refs := jstrs(rule, "variant_refs")
		ours := len(refs) == 1 && slices.ContainsFunc(platforms, func(e domain.PlatformPrice) bool { return e.VariantRef == refs[0] })
		if !ours {
			kept = append(kept, rule)
		}
	}
	for _, e := range platforms {
		kept = append(kept, jobj{"variant_refs": []any{e.VariantRef}, "price": money(e.Apply(price))})
	}
	o["price_overrides"] = objsToAny(kept)
}

// mergeHiddenRestriction takes a hidden SKU out of the platforms' price lists and puts it back when
// it is visible again. Without a price list the bridges push every SKU, so a hidden one stays
// orderable on a platform: the plan says so.
func (p *pubPlanner) mergeHiddenRestriction(s jobj, d *dProduct) {
	r, _ := s["restrictions"].(jobj)
	if !d.local.Visible {
		if len(p.in.Settings.PlatformPrices) == 0 {
			p.warn(domain.WarnHiddenOnPlatforms, d.sku, "the hidden product "+d.sku+" stays orderable on the platforms until they read a platform price list")
			return
		}
		if r == nil {
			r = jobj{}
		}
		r["variant_refs"] = []any{hiddenVariantRef}
		s["restrictions"] = r
		return
	}
	if r == nil || !slices.Equal(jstrs(r, "variant_refs"), []string{hiddenVariantRef}) {
		return
	}
	delete(r, "variant_refs")
	if len(r) == 0 {
		delete(s, "restrictions")
	}
}

// ---- products and SKUs ----

var hiddenTags = []string{"hidden", "web:hidden"}

// vatRates maps a VAT category to the tax rates of a product (delivery, collection, eat in).
func vatRates(vat string) (jobj, bool) {
	switch vat {
	case domain.VatFood:
		return jobj{"delivery": "6", "collection": "6", "eat_in": "12"}, true
	case domain.VatBeverage:
		return jobj{"delivery": "21", "collection": "21", "eat_in": "21"}, true
	case domain.VatZeroRated:
		return jobj{"delivery": "0", "collection": "0", "eat_in": "0"}, true
	}
	return nil, false
}

func taxRateEqual(a, b jobj) bool {
	for _, k := range []string{"delivery", "collection", "eat_in"} {
		x, err1 := hubrise.ParseTaxRate(jstr(a, k))
		y, err2 := hubrise.ParseTaxRate(jstr(b, k))
		if err1 != nil || err2 != nil || !x.Equal(y) {
			return false
		}
	}
	return true
}

func (p *pubPlanner) mergeProducts(out *baseCatalog) {
	bySku := map[string]*dProduct{}
	for _, d := range p.prods {
		bySku[d.sku] = d
	}
	dealSkus := map[string]bool{}
	for _, deal := range out.deals {
		for _, line := range jobjs(deal, "lines") {
			for _, s := range jobjs(line, "skus") {
				dealSkus[jstr(s, "ref")] = true
			}
		}
	}
	placed := map[string]bool{}
	result := make([]jobj, 0, len(out.products))
	lastOwned := -1
	for _, bp := range out.products {
		baseSkus := jobjs(bp, "skus")
		single := len(baseSkus) == 1 && strings.TrimSpace(jstr(baseSkus[0], "name")) == ""
		var here []*dProduct
		kept := make([]any, 0, len(baseSkus))
		for _, s := range baseSkus {
			ref := jstr(s, "ref")
			switch d := bySku[ref]; {
			case d != nil && ref != "":
				here = append(here, d)
				placed[ref] = true
				kept = append(kept, s)
			case ref != "" && p.removeSku[ref]:
				if dealSkus[ref] {
					r, _ := s["restrictions"].(jobj)
					if r == nil {
						r = jobj{}
					}
					r["enabled"] = false
					s["restrictions"] = r
					p.retained[ref] = true
					p.warn(domain.WarnRetainedByDeal, ref, "the SKU "+ref+" is gone from the shop but a HubRise deal uses it, so it is kept, disabled")
					kept = append(kept, s)
					continue
				}
				p.removedSkus[ref] = true
				p.baseSkuName[ref] = skuFullName(bp, s)
			default:
				kept = append(kept, s)
			}
		}
		if len(kept) == 0 {
			continue
		}
		hadHidden := hasAnyTag(tagSet(bp), hiddenTags)
		if len(here) > 0 {
			p.mergeProductLevel(bp, here, single)
		}
		skuObjs := make([]any, 0, len(kept))
		for _, k := range kept {
			s := k.(jobj)
			if d := bySku[jstr(s, "ref")]; d != nil && jstr(s, "ref") != "" {
				s = p.mergeSku(s, d, !single, jstr(bp, "name"))
			} else if hadHidden && len(here) > 0 {
				addTag(s, "web:hidden")
			}
			skuObjs = append(skuObjs, s)
		}
		bp["skus"] = skuObjs
		if hadHidden {
			removeTags(bp, hiddenTags)
		}
		result = append(result, bp)
		if len(here) > 0 {
			lastOwned = len(result) - 1
		}
	}
	var fresh []jobj
	for _, d := range p.prods {
		if placed[d.sku] {
			continue
		}
		fresh = append(fresh, p.newProduct(d))
	}
	if lastOwned < 0 {
		lastOwned = len(result) - 1
	}
	out.products = append(result[:lastOwned+1:lastOwned+1], append(fresh, result[lastOwned+1:]...)...)
	p.plan.RemovedSkus = len(p.removedSkus)
}

func hasAnyTag(tags, wanted []string) bool {
	return slices.ContainsFunc(wanted, func(w string) bool { return slices.Contains(tags, w) })
}

func addTag(o jobj, tag string) {
	if slices.Contains(tagSet(o), tag) {
		return
	}
	tags, _ := o["tags"].([]any)
	o["tags"] = append(tags, tag)
}

func removeTags(o jobj, drop []string) {
	tags, ok := o["tags"].([]any)
	if !ok {
		return
	}
	kept := make([]any, 0, len(tags))
	for _, t := range tags {
		if s, ok := t.(string); ok && slices.Contains(drop, strings.ToLower(strings.TrimSpace(s))) {
			continue
		}
		kept = append(kept, t)
	}
	o["tags"] = kept
}

// mergeProductLevel writes the fields of the HubRise product the shop owns.
func (p *pubPlanner) mergeProductLevel(bp jobj, here []*dProduct, single bool) {
	first := here[0]
	p.mergeImages(bp, here)
	if single {
		bp["name"] = first.name
		if first.description != "" {
			bp["description"] = first.description
		} else {
			delete(bp, "description")
		}
	}
	agree := func(get func(*dProduct) string) (string, bool) {
		v := get(first)
		for _, d := range here[1:] {
			if get(d) != v {
				return "", false
			}
		}
		return v, true
	}
	if ref, ok := agree(func(d *dProduct) string { return d.cat.ref }); ok {
		bp["category_ref"] = ref
	} else {
		p.warn(domain.WarnCategoryDiverges, first.sku, "the SKUs of one HubRise product are in different categories, the HubRise category is kept")
	}
	vat, ok := agree(func(d *dProduct) string { return d.local.VatCategory })
	if !ok {
		p.warn(domain.WarnCategoryDiverges, first.sku, "the SKUs of one HubRise product have different VAT categories, the HubRise tax rate is kept")
		return
	}
	if rates, expressible := vatRates(vat); expressible {
		if cur, _ := bp["tax_rate"].(jobj); cur == nil || !taxRateEqual(cur, rates) {
			bp["tax_rate"] = rates
		}
	}
}

// splitName splits the local name of a SKU of a multi-SKU product into the part after the product
// name. ok is false when the local name does not start with the product name.
func splitName(productName, local string) (skuName string, ok bool) {
	productName, local = strings.TrimSpace(productName), strings.TrimSpace(local)
	switch {
	case local == productName:
		return "", true
	case strings.HasPrefix(local, productName+" "):
		return strings.TrimSpace(strings.TrimPrefix(local, productName)), true
	}
	return local, false
}

// effectiveOverride returns the index of the price rule the importer resolves for the variant: the
// last unconditional rule that applies (mapper.price), or -1.
func effectiveOverride(overrides []jobj, variant string) int {
	idx := -1
	for i, o := range overrides {
		if jstr(o, "dow") != "" || jstr(o, "start_time") != "" || jstr(o, "end_time") != "" ||
			jstr(o, "start_date") != "" || jstr(o, "end_date") != "" ||
			len(jstrs(o, "service_types")) > 0 || len(jstrs(o, "service_type_refs")) > 0 {
			continue
		}
		refs := jstrs(o, "variant_refs")
		if len(refs) > 0 && (variant == "" || !slices.Contains(refs, variant)) {
			continue
		}
		idx = i
	}
	return idx
}

// mergeSku writes the owned fields of a SKU. multi says the HubRise product has other SKUs (or this
// one is named), so the local name is split into the product name and the SKU name.
func (p *pubPlanner) mergeSku(src jobj, d *dProduct, multi bool, productName string) jobj {
	s := cloneObj(src)
	s["ref"] = d.sku
	price := money(d.local.Price)
	overrides := jobjs(s, "price_overrides")
	if i := effectiveOverride(overrides, p.in.Settings.VariantRef); i >= 0 {
		overrides[i]["price"] = price
	} else {
		s["price"] = price
	}
	p.writePlatformPrices(s, d.local.Price)
	if multi {
		name, ok := splitName(productName, d.name)
		if !ok {
			p.warn(domain.WarnNameSplitAmbiguous, d.sku, "the name \""+d.name+"\" does not start with the HubRise product name \""+productName+"\", it became the SKU name")
		}
		if name != "" {
			s["name"] = name
		} else {
			delete(s, "name")
		}
	}

	// Visibility: web:hidden is the tag the importer reads as hidden.
	if d.local.Visible {
		removeTags(s, hiddenTags)
	} else {
		addTag(s, "web:hidden")
	}
	p.mergeHiddenRestriction(s, d)

	cf, _ := s["custom_fields"].(jobj)
	if cf == nil {
		cf = jobj{}
	}
	if code := strings.TrimSpace(d.local.Code); code != "" {
		cf["code"] = code
	} else {
		delete(cf, "code")
	}
	if d.local.VatCategory != "" {
		cf["vat_category"] = d.local.VatCategory
	} else {
		delete(cf, "vat_category")
	}
	tr, _ := cf["translations"].(jobj)
	for lang, t := range d.local.Texts {
		name := strings.TrimSpace(t.Name)
		if name == "" {
			continue
		}
		if tr == nil {
			tr = jobj{}
		}
		entry, _ := tr[lang].(jobj)
		if entry == nil {
			entry = jobj{}
		}
		entry["name"] = name
		if desc := strings.TrimSpace(t.Description); desc != "" {
			entry["description"] = desc
		} else {
			delete(entry, "description")
		}
		tr[lang] = entry
	}
	if tr != nil {
		cf["translations"] = tr
	}
	if len(cf) > 0 || src["custom_fields"] != nil {
		s["custom_fields"] = cf
	}

	// Option lists: the shop's first, in group order, then the lists it does not own.
	owned := map[string]bool{}
	refs := make([]any, 0, len(d.groups))
	for _, g := range d.groups {
		refs = append(refs, g.listRef)
		owned[g.listRef] = true
	}
	for _, it := range p.publishedKeys[domain.EntityChoiceGroup] {
		if strings.HasPrefix(it.HubriseKey, d.sku+"|") {
			owned[it.HubriseRef] = true
		}
	}
	for _, g := range d.groups {
		owned[g.base0] = true
	}
	for _, r := range jstrs(s, "option_list_refs") {
		if !owned[r] {
			refs = append(refs, r)
		}
	}
	if len(refs) > 0 || src["option_list_refs"] != nil {
		s["option_list_refs"] = refs
	}
	return s
}

// mergeImages writes the image ids of the products the shop decided on into the image_ids of the
// HubRise product: the ids of their earlier images go, the ids of anybody else stay, the new ones
// come last. A product the shop knows nothing about (hidden, image unreadable) keeps its ids.
func (p *pubPlanner) mergeImages(bp jobj, here []*dProduct) {
	im := p.in.Images
	if im == nil {
		return
	}
	decided := map[uuid.UUID]bool{}
	for _, d := range here {
		if _, ok := im.Desired[d.local.ID]; ok {
			decided[d.local.ID] = true
		}
	}
	if len(decided) == 0 {
		return
	}
	ids := make([]any, 0, len(jstrs(bp, "image_ids")))
	for _, id := range jstrs(bp, "image_ids") {
		if owner, ok := im.Owned[id]; ok && decided[owner] {
			continue
		}
		ids = append(ids, id)
	}
	for _, d := range here {
		if id := im.Desired[d.local.ID]; id != "" && !slices.Contains(ids, any(id)) {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		delete(bp, "image_ids")
		return
	}
	bp["image_ids"] = ids
}

func (p *pubPlanner) newProduct(d *dProduct) jobj {
	pr := jobj{"ref": d.sku, "name": d.name, "category_ref": d.cat.ref}
	if p.in.Images != nil {
		if id := p.in.Images.Desired[d.local.ID]; id != "" {
			pr["image_ids"] = []any{id}
		}
	}
	if d.description != "" {
		pr["description"] = d.description
	}
	if rates, ok := vatRates(d.local.VatCategory); ok {
		pr["tax_rate"] = rates
	} else {
		p.warn(domain.WarnVatNotExpressible, d.sku, "the VAT category "+d.local.VatCategory+" has no tax rate; only the SKU custom field carries it")
	}
	pr["skus"] = []any{p.mergeSku(nil, d, false, d.name)}
	return pr
}

// ---- removal of what nothing uses any more ----

func (p *pubPlanner) dropUnreferenced(out *baseCatalog) {
	usedLists := map[string]bool{}
	for _, pr := range out.products {
		for _, s := range jobjs(pr, "skus") {
			for _, r := range jstrs(s, "option_list_refs") {
				usedLists[r] = true
			}
		}
	}
	out.lists = slices.DeleteFunc(out.lists, func(l jobj) bool {
		ref := refOf(l)
		return p.removedList[ref] && p.listByRef[ref] == nil && !usedLists[ref]
	})

	usedCats := map[string]bool{}
	for _, pr := range out.products {
		usedCats[jstr(pr, "category_ref")] = true
	}
	for _, d := range out.deals {
		usedCats[jstr(d, "category_ref")] = true
	}
	for _, c := range out.categories {
		if parent := jstr(c, "parent_ref"); parent != "" && !p.removeCat[jstr(c, "ref")] {
			usedCats[parent] = true
		}
	}
	for _, ref := range slices.Sorted(maps.Keys(p.removeCat)) {
		if p.catKept(ref) {
			continue
		}
		if usedCats[ref] {
			if !p.foreignCat[ref] {
				p.warn(domain.WarnCategoryInUse, ref, "the category "+ref+" is gone from the shop but something at HubRise still uses it, so it is kept")
			}
			delete(p.removeCat, ref)
		}
	}
}

func (p *pubPlanner) catKept(ref string) bool {
	for _, c := range p.cats {
		if c.ref == ref {
			return true
		}
	}
	return false
}

// ---- categories ----

func (p *pubPlanner) mergeCategories(out *baseCatalog) {
	baseByRef := map[string]jobj{}
	for _, c := range out.categories {
		baseByRef[refOf(c)] = c
	}
	desired := make([]jobj, 0, len(p.cats))
	for _, d := range p.cats {
		c := cloneObj(baseByRef[d.ref])
		c["ref"] = d.ref
		c["name"] = d.name
		p.mergeCategoryNames(c, d)
		desired = append(desired, c)
	}
	out.categories = placeOwned(out.categories, refOf, desired, p.removeCat)
}

// categoryNameTagPrefix starts the tags that carry a category's name in another language:
// name:zh:寿司. HubRise categories have no custom fields, and their description reaches the customers
// on the platforms.
const categoryNameTagPrefix = "name:"

// mergeCategoryNames writes the names of the other languages as tags, and removes what earlier
// versions left in the description for the same purpose. A description of the restaurant stays.
func (p *pubPlanner) mergeCategoryNames(c jobj, d *dCategory) {
	tags := []any{}
	for _, t := range jstrs(c, "tags") {
		if !strings.HasPrefix(t, categoryNameTagPrefix) {
			tags = append(tags, t)
		}
	}
	langs := make([]string, 0, len(d.names))
	for lang := range d.names {
		if lang != p.lang {
			langs = append(langs, lang)
		}
	}
	sort.Strings(langs)
	for _, lang := range langs {
		tags = append(tags, categoryNameTagPrefix+lang+":"+d.names[lang])
	}
	// HubRise answers with an empty list: writing none where there was one is no change.
	if _, had := c["tags"]; had || len(tags) > 0 {
		c["tags"] = tags
	}
	if payload, err := decodeObject([]byte(strings.TrimSpace(jstr(c, "description")))); err == nil && payload[categoryTranslationsKey] != nil {
		delete(c, "description")
	}
}
