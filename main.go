package main

import (
	"flag"
	"fmt"
	"go/build"
	"log"
	"os"
	"sort"
	"strings"
)

var (
	pkgs        map[string]*build.Package
	ids         map[string]int
	colorSubst  map[string]string
	prefixSubst map[string]string

	nextId int

	ignored = map[string]bool{
		"C": true,
	}
	ignoredPrefixes []string
	onlyPrefixes    []string

	ignoreStdlib       = flag.Bool("s", false, "ignore packages in the Go standard library")
	delveGoroot        = flag.Bool("d", false, "show dependencies of packages in the Go standard library")
	ignorePrefixes     = flag.String("p", "", "a comma-separated list of prefixes to ignore")
	ignorePackages     = flag.String("i", "", "a comma-separated list of packages to ignore")
	onlyPrefix         = flag.String("o", "", "a comma-separated list of prefixes to include")
	tagList            = flag.String("tags", "", "a comma-separated list of build tags to consider satisified during the build")
	horizontal         = flag.Bool("horizontal", false, "lay out the dependency graph horizontally instead of vertically")
	includeTests       = flag.Bool("t", false, "include test packages")
	maxLevel           = flag.Int("l", 256, "max level of go dependency graph")
	prefixSubstitution = flag.String("r", "", "a comma-separeated list of prefix replacement, e.g. github.com=g")
	colorSpec          = flag.String("c", "", "a comma-separated list of color spec, e.g. github.com=red")

	buildTags    []string
	buildContext = build.Default
)

func main() {
	pkgs = make(map[string]*build.Package)
	ids = make(map[string]int)
	colorSubst = make(map[string]string)
	prefixSubst = make(map[string]string)
	flag.Parse()

	args := flag.Args()

	if len(args) < 1 {
		log.Fatal("need one package name to process")
	}

	if *ignorePrefixes != "" {
		ignoredPrefixes = strings.Split(*ignorePrefixes, ",")
	}
	if *onlyPrefix != "" {
		onlyPrefixes = strings.Split(*onlyPrefix, ",")
	}
	if *ignorePackages != "" {
		for _, p := range strings.Split(*ignorePackages, ",") {
			ignored[p] = true
		}
	}
	if *tagList != "" {
		buildTags = strings.Split(*tagList, ",")
	}
	buildContext.BuildTags = buildTags

	if *colorSpec != "" {
		colors := strings.Split(*colorSpec, ",")
		for _, c := range colors {
			spec := strings.Split(c, "=")
			if len(spec) != 2 {
				log.Fatalf("wrong color spec: %s", c)
			}
			colorSubst[spec[0]] = spec[1]
		}
	}

	if *prefixSubstitution != "" {
		prefixes := strings.Split(*prefixSubstitution, ",")
		for _, p := range prefixes {
			spec := strings.Split(p, "=")
			specLen := len(spec)
			if specLen < 1 || specLen > 2 {
				log.Fatalf("wrong prefix substitution spec: %s", spec)
			} else if specLen == 1 {
				prefixSubst[spec[0]] = ""
			} else if specLen == 2 {
				prefixSubst[spec[0]] = spec[1]
			}
		}
	}

	cwd, err := os.Getwd()
	if err != nil {
		log.Fatalf("failed to get cwd: %s", err)
	}
	for _, a := range args {
		if err := processPackage(cwd, a, 0); err != nil {
			log.Fatal(err)
		}
	}

	fmt.Println("digraph godep {")
	if *horizontal {
		fmt.Println(`rankdir="LR"`)
	}

	// sort packages
	pkgKeys := []string{}
	for k := range pkgs {
		pkgKeys = append(pkgKeys, k)
	}
	sort.Strings(pkgKeys)

	for _, pkgName := range pkgKeys {
		pkg := pkgs[pkgName]
		pkgId := getId(pkgName)

		if isIgnored(pkg) {
			continue
		}

		var color string
		if pkg.Goroot {
			color = "palegreen"
		} else if len(pkg.CgoFiles) > 0 {
			color = "darkgoldenrod1"
		} else {
			color = "paleturquoise"
		}

		fmt.Printf("_%d [label=\"%s\" style=\"filled\" color=\"%s\"];\n", pkgId, processName(pkgName), processColor(pkgName, color))

		// Don't render imports from packages in Goroot
		if pkg.Goroot && !*delveGoroot {
			continue
		}

		for _, imp := range getImports(pkg) {
			impPkg := pkgs[imp]
			if impPkg == nil || isIgnored(impPkg) {
				continue
			}

			impId := getId(imp)
			fmt.Printf("_%d -> _%d;\n", pkgId, impId)
		}
	}
	fmt.Println("}")
}

func processColor(name, color string) string {
	foundPrefixLen := 0
	for prefix, c := range colorSubst {
		if strings.HasPrefix(name, prefix) && len(prefix) > foundPrefixLen {
			color = c
			foundPrefixLen = len(prefix)
		}
	}
	return color
}

func processName(name string) string {
	newName := name
	foundPrefix := 0
	for prefix, subst := range prefixSubst {
		if strings.HasPrefix(name, prefix) && len(prefix) > foundPrefix {
			newName = subst + strings.TrimPrefix(name, prefix)
			foundPrefix = len(prefix)
		}
	}
	return newName
}

func processPackage(root string, pkgName string, level int) error {
	if level++; level > *maxLevel {
		return nil
	}
	if ignored[pkgName] {
		return nil
	}

	pkg, err := buildContext.Import(pkgName, root, 0)
	if err != nil {
		return fmt.Errorf("failed to import %s: %s", pkgName, err)
	}

	if isIgnored(pkg) {
		return nil
	}

	pkgs[normalizeVendor(pkg.ImportPath)] = pkg

	// Don't worry about dependencies for stdlib packages
	if pkg.Goroot && !*delveGoroot {
		return nil
	}

	for _, imp := range getImports(pkg) {
		if _, ok := pkgs[imp]; !ok {
			if err := processPackage(pkg.Dir, imp, level); err != nil {
				return err
			}
		}
	}
	return nil
}

func getImports(pkg *build.Package) []string {
	allImports := pkg.Imports
	if *includeTests {
		allImports = append(allImports, pkg.TestImports...)
		allImports = append(allImports, pkg.XTestImports...)
	}
	var imports []string
	found := make(map[string]struct{})
	for _, imp := range allImports {
		if imp == normalizeVendor(pkg.ImportPath) {
			// Don't draw a self-reference when foo_test depends on foo.
			continue
		}
		if _, ok := found[imp]; ok {
			continue
		}
		found[imp] = struct{}{}
		imports = append(imports, imp)
	}
	return imports
}

func getId(name string) int {
	id, ok := ids[name]
	if !ok {
		id = nextId
		nextId++
		ids[name] = id
	}
	return id
}

func hasPrefixes(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

func isIgnored(pkg *build.Package) bool {
	if len(onlyPrefixes) > 0 && !hasPrefixes(normalizeVendor(pkg.ImportPath), onlyPrefixes) {
		return true
	}
	return ignored[normalizeVendor(pkg.ImportPath)] || (pkg.Goroot && *ignoreStdlib) || hasPrefixes(normalizeVendor(pkg.ImportPath), ignoredPrefixes)
}

func debug(args ...interface{}) {
	fmt.Fprintln(os.Stderr, args...)
}

func debugf(s string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, s, args...)
}

func normalizeVendor(path string) string {
	pieces := strings.Split(path, "vendor/")
	return pieces[len(pieces)-1]
}
