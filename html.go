package main

import (
	"html/template"
	"log"
	"os"
)

func writeTemplateToHTML(templateData TemplateData) {
	if _, err := os.Stat(outputDir); os.IsNotExist(err) {
		os.Mkdir(outputDir, os.ModePerm)
	}
	f, err := os.Create(outputDir + string(os.PathSeparator) + templateName + ".html")
	if err != nil {
		log.Println("writeTemplateToHTML", err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			log.Println("writeTemplateToHTML", err)
		}
	}()

	htmlSource, err := template.New(templateName + templateFileEnding).Funcs(template.FuncMap{
		"unescapeHTML": func(input string) template.HTML {
			return template.HTML(input)
		},
	}).ParseFiles(tmplDirectory + "/" + templateName + templateFileEnding)
	if err != nil {
		log.Println("writeTemplateToHTML", err)
	}
	htmlSource.Execute(f, templateData)
}
